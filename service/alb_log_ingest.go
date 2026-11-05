package service

import (
	"bufio"
	"compress/gzip"
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

// UNIFYAPI-FORK: pull the load balancer's own logs into the platform log.
//
// The ALB writes two kinds of gzip to S3 and neither is reachable from the
// console: access logs (one line per request that became a request) and
// connection logs (one line per connection, including the ones that never
// did). The second is the only record of a caller who got no response at all,
// which is exactly what a customer reported on 2026-11-04 and what nothing in
// this product could confirm or deny.
//
// Only failures are ingested. Successful traffic is already in `logs` with far
// more detail, and at ~5k requests/day copying it here would bury the handful
// of lines that matter.

const (
	albIngestInterval = 5 * time.Minute
	// Objects are delivered every 5 minutes but not instantly, and a poll can
	// be late. Re-reading is free because every row carries a Dedupe key, so
	// the window overlaps rather than racing the delivery.
	albIngestLookback = 20 * time.Minute
)

type albLogIngester struct {
	client *s3.Client
	bucket string
	prefix string
	// Objects already handled. Bounded by the lookback window, not by history:
	// an object older than the window is never listed again.
	seen map[string]time.Time
}

// StartALBLogIngest is a no-op unless ALB_LOG_BUCKET is set, so a developer
// machine and a self-hosted install are unaffected.
func StartALBLogIngest() {
	bucket := os.Getenv("ALB_LOG_BUCKET")
	if bucket == "" {
		return
	}
	region := os.Getenv("ALB_LOG_REGION")
	if region == "" {
		common.SysLog("[alb-ingest] ALB_LOG_BUCKET set without ALB_LOG_REGION; not starting")
		return
	}

	cfg, err := awsconfig.LoadDefaultConfig(context.Background(), awsconfig.WithRegion(region))
	if err != nil {
		common.SysLog(fmt.Sprintf("[alb-ingest] cannot load AWS config: %v", err))
		return
	}

	ing := &albLogIngester{
		client: s3.NewFromConfig(cfg),
		bucket: bucket,
		prefix: os.Getenv("ALB_LOG_PREFIX"),
		seen:   map[string]time.Time{},
	}
	common.SysLog(fmt.Sprintf("[alb-ingest] watching s3://%s/%s in %s", bucket, ing.prefix, region))

	go func() {
		for {
			// Ingest must never be able to take down the console it observes.
			func() {
				defer func() {
					if r := recover(); r != nil {
						common.SysLog(fmt.Sprintf("[alb-ingest] panicked: %v", r))
					}
				}()
				if n, err := ing.pollOnce(context.Background()); err != nil {
					common.SysLog(fmt.Sprintf("[alb-ingest] poll failed: %v", err))
				} else if n > 0 {
					common.SysLog(fmt.Sprintf("[alb-ingest] recorded %d events", n))
				}
			}()
			time.Sleep(albIngestInterval)
		}
	}()
}

func (i *albLogIngester) pollOnce(ctx context.Context) (int, error) {
	cutoff := time.Now().Add(-albIngestLookback)
	for k, t := range i.seen {
		if t.Before(cutoff) {
			delete(i.seen, k)
		}
	}

	var events []model.PlatformEvent
	p := s3.NewListObjectsV2Paginator(i.client, &s3.ListObjectsV2Input{
		Bucket: aws.String(i.bucket),
		Prefix: aws.String(i.prefix),
	})
	for p.HasMorePages() {
		page, err := p.NextPage(ctx)
		if err != nil {
			return 0, err
		}
		for _, obj := range page.Contents {
			key := aws.ToString(obj.Key)
			mod := aws.ToTime(obj.LastModified)
			if mod.Before(cutoff) {
				continue
			}
			if _, done := i.seen[key]; done {
				continue
			}
			lines, err := i.fetchLines(ctx, key)
			if err != nil {
				// One unreadable object must not stop the rest; it will be
				// retried while it is still inside the lookback window.
				common.SysLog(fmt.Sprintf("[alb-ingest] %s: %v", key, err))
				continue
			}
			i.seen[key] = mod
			if strings.Contains(key, "conn_log_") {
				events = append(events, parseConnectionLog(lines)...)
			} else if strings.HasSuffix(key, ".log.gz") {
				events = append(events, parseAccessLog(lines)...)
			}
		}
	}
	if len(events) == 0 {
		return 0, nil
	}
	return len(events), model.RecordPlatformEvents(events)
}

func (i *albLogIngester) fetchLines(ctx context.Context, key string) ([]string, error) {
	out, err := i.client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(i.bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		return nil, err
	}
	defer out.Body.Close()

	gz, err := gzip.NewReader(out.Body)
	if err != nil {
		return nil, err
	}
	defer gz.Close()

	var lines []string
	sc := bufio.NewScanner(gz)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		if line := strings.TrimSpace(sc.Text()); line != "" {
			lines = append(lines, line)
		}
	}
	return lines, sc.Err()
}

// A connection log line is plain space-separated:
//
//	1 time  2 client_ip  3 client_port  4 listener_port  5 tls_protocol
//	6 tls_cipher  7 handshake_latency  8 leaf_cert_subject  9/10 cert validity
//	11 tls_verify_status  12 conn_trace_id  13 named_group  14 alb  15 alb_node
//
// Two different failures live in here and collapsing them loses the one that
// matters. Measured 2026-11-05:
//
//	115.207.110.183  port 443, protocol "-", status "-"
//	    The handshake never completed, so there is no protocol to report. This
//	    is what ClientTLSNegotiationErrorCount counts, and the only shape that
//	    produces the "no response at all" a customer describes.
//
//	38.145.218.25  port 443, TLSv1.3, a named cipher, Failed:UnmappedConnectionError
//	    TLS was negotiated and the connection died afterwards. CloudWatch does
//	    NOT count this one, and the client did get a connection -- a different
//	    root cause with a different fix.
//
// Port 443 has to be checked first. On fields 5, 6 and 11 alone a plain HTTP
// connection to the port 80 listener is indistinguishable from the first shape:
// scanners produce a steady trickle of them and they are not failures at all.
func parseConnectionLog(lines []string) []model.PlatformEvent {
	var out []model.PlatformEvent
	for _, line := range lines {
		f := strings.Fields(line)
		if len(f) < 12 {
			continue
		}
		if f[3] != "443" {
			continue
		}
		protocol, verify := f[4], f[10]
		handshakeNeverStarted := protocol == "-"
		failedAfterHandshake := strings.HasPrefix(verify, "Failed")
		if !handshakeNeverStarted && !failedAfterHandshake {
			continue
		}

		ts, err := time.Parse(time.RFC3339Nano, f[0])
		if err != nil {
			continue
		}
		kind := model.PlatformEventKindTLSConnectionFailed
		detail := "TLS negotiated, connection failed afterwards: " + verify
		if handshakeNeverStarted {
			kind = model.PlatformEventKindTLSHandshakeFailed
			detail = "handshake did not complete; no protocol was agreed"
		}
		out = append(out, model.PlatformEvent{
			CreatedAt:   ts.Unix(),
			Source:      model.PlatformEventSourceALBConn,
			Kind:        kind,
			Severity:    "warn",
			ClientIP:    f[1],
			TLSProtocol: nullDash(protocol),
			TLSCipher:   nullDash(f[5]),
			Detail:      detail,
			// The connection trace id is unique per connection.
			Dedupe: "conn:" + f[11],
		})
	}
	return out
}

// An access log line is space-separated with quoted fields; only the leading
// fixed columns are needed here:
//
//	1 type  2 time  3 alb  4 client:port  5 target:port  6/7/8 processing times
//	9 elb_status_code  10 target_status_code  ...  13 "request"  14 "user_agent"
//
// Successful requests are deliberately skipped: they are already in `logs`,
// with the model, the tokens and the money, none of which the access log knows.
func parseAccessLog(lines []string) []model.PlatformEvent {
	var out []model.PlatformEvent
	for _, line := range lines {
		f := strings.Fields(line)
		if len(f) < 13 {
			continue
		}
		status, err := strconv.Atoi(f[8])
		if err != nil || (status >= 200 && status < 400) {
			continue
		}
		ts, err := time.Parse(time.RFC3339Nano, f[1])
		if err != nil {
			continue
		}
		method, path := parseQuotedRequest(line)
		out = append(out, model.PlatformEvent{
			CreatedAt: ts.Unix(),
			Source:    model.PlatformEventSourceALBAccess,
			Kind:      model.PlatformEventKindHTTPRejected,
			Severity:  "warn",
			ClientIP:  hostOnly(f[3]),
			Method:    method,
			Path:      path,
			Status:    status,
			Detail:    "target status " + f[9],
			// trace_id is unique per request and is the field AWS tells you to
			// quote in a support case. It is not guaranteed present, and an
			// empty one would give EVERY such row the same key -- the conflict
			// clause would then keep exactly one of them and silently drop the
			// rest, which looks identical to there having been only one.
			Dedupe: accessDedupe(line, ts, f[3], status),
		})
	}
	return out
}

func parseQuotedRequest(line string) (method, path string) {
	parts := strings.Split(line, `"`)
	if len(parts) < 2 {
		return "", ""
	}
	req := strings.Fields(parts[1])
	if len(req) < 2 {
		return "", ""
	}
	method = req[0]
	path = req[1]
	if u := strings.Index(path, "://"); u >= 0 {
		if slash := strings.Index(path[u+3:], "/"); slash >= 0 {
			path = path[u+3+slash:]
		}
	}
	if q := strings.Index(path, "?"); q >= 0 {
		path = path[:q]
	}
	if len(path) > 255 {
		path = path[:255]
	}
	return method, path
}

func accessDedupe(line string, ts time.Time, clientPort string, status int) string {
	if id := traceID(line); id != "" {
		return "access:" + id
	}
	// Client port is reused slowly enough that it separates connections within
	// the same nanosecond timestamp.
	return fmt.Sprintf("access:%d:%s:%d", ts.UnixNano(), clientPort, status)
}

func traceID(line string) string {
	for _, f := range strings.Fields(line) {
		if strings.HasPrefix(f, `"Root=`) || strings.HasPrefix(f, "Root=") {
			return strings.Trim(f, `"`)
		}
	}
	return ""
}

func hostOnly(hostPort string) string {
	if i := strings.LastIndex(hostPort, ":"); i > 0 {
		return hostPort[:i]
	}
	return hostPort
}

func nullDash(s string) string {
	if s == "-" {
		return ""
	}
	return s
}
