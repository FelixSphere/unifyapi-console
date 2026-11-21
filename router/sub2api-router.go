package router

import (
	"bytes"
	"context"
	"errors"
	"io"
	"mime"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"path"
	"strconv"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/gin-gonic/gin"
)

const sub2apiPrefix = "/sub2api"

type sub2apiClientIPKey struct{}

// This namespace uses Sub2API's model keys and billing. It must never proxy
// the upstream console, account API, or our own session cookies.
func setSub2APIRouter(engine *gin.Engine) {
	target, err := url.Parse(strings.TrimSpace(os.Getenv("UNIFYAPI_SUB2API_GATEWAY_URL")))
	if err != nil || target.Hostname() == "" || (target.Scheme != "https" && target.Scheme != "http") ||
		target.User != nil || target.RawQuery != "" || target.ForceQuery || target.Fragment != "" ||
		(target.Path != "" && target.Path != "/") {
		if os.Getenv("UNIFYAPI_SUB2API_GATEWAY_URL") != "" {
			common.SysLog("invalid UNIFYAPI_SUB2API_GATEWAY_URL; Sub2API gateway disabled")
		}
		return
	}

	proxy := &httputil.ReverseProxy{
		Rewrite: func(r *httputil.ProxyRequest) {
			r.SetURL(target)
			r.Out.URL.Path = strings.TrimPrefix(r.In.URL.Path, sub2apiPrefix)
			r.Out.URL.RawPath = ""
			for name := range r.Out.Header {
				switch name {
				case "Authorization", "X-Api-Key", "X-Goog-Api-Key", "Anthropic-Version", "Anthropic-Beta",
					"Openai-Beta", "Openai-Organization", "Openai-Project", "Accept",
					"Content-Type", "Content-Encoding", "Range", "If-Range", "User-Agent", "Connection", "Upgrade",
					"Sec-Websocket-Key", "Sec-Websocket-Version", "Sec-Websocket-Protocol", "Sec-Websocket-Extensions",
					"X-Client-Request-Id", "Idempotency-Key":
				default:
					r.Out.Header.Del(name)
				}
			}
			if ip, ok := r.In.Context().Value(sub2apiClientIPKey{}).(string); ok {
				r.Out.Header.Set("X-Forwarded-For", ip)
			}
		},
		ModifyResponse: func(response *http.Response) error {
			mediaType, _, _ := mime.ParseMediaType(response.Header.Get("Content-Type"))
			location := response.Header.Get("Location")
			if strings.EqualFold(mediaType, "text/html") || (location != "" && !isSub2APIModelPath(location)) {
				return errors.New("unexpected Sub2API gateway response")
			}
			for name := range response.Header {
				switch name {
				case "Content-Type", "Content-Length", "Content-Disposition", "Content-Encoding", "Content-Range",
					"Accept-Ranges", "Etag", "Last-Modified", "Retry-After", "X-Request-Id",
					"Connection", "Upgrade", "Sec-Websocket-Accept", "Sec-Websocket-Protocol", "Sec-Websocket-Extensions":
				default:
					response.Header.Del(name)
				}
			}
			if location != "" {
				response.Header.Set("Location", sub2apiPrefix+location)
			}
			if response.StatusCode == http.StatusAccepted && strings.HasPrefix(location, "/v1/images/tasks/") {
				if err := prefixSub2APIImagePollURL(response, location); err != nil {
					return err
				}
			}
			response.Header.Set("Cache-Control", "private, no-store")
			response.Header.Set("Content-Security-Policy", "sandbox; default-src 'none'")
			response.Header.Set("X-Content-Type-Options", "nosniff")
			response.Header.Set("X-Accel-Buffering", "no")
			return nil
		},
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadGateway)
			_, _ = w.Write([]byte(`{"error":{"message":"Sub2API gateway unavailable","type":"upstream_error"}}`))
		},
	}

	gateway := engine.Group(sub2apiPrefix, middleware.RouteTag("relay"),
		middleware.SystemPerformanceCheck(), middleware.RelayConcurrencyLimit())
	gateway.Any("/*path", func(c *gin.Context) {
		upstreamPath := c.Param("path")
		if !isSub2APIModelPath(upstreamPath) || c.Request.URL.Path != sub2apiPrefix+upstreamPath {
			c.JSON(http.StatusNotFound, gin.H{"error": gin.H{"message": "Sub2API model endpoint not found"}})
			return
		}
		request := c.Request
		if ip := net.ParseIP(c.ClientIP()); ip != nil {
			request = request.WithContext(context.WithValue(request.Context(), sub2apiClientIPKey{}, ip.String()))
		}
		proxy.ServeHTTP(c.Writer, request)
	})
}

func isSub2APIModelPath(value string) bool {
	if path.Clean(value) != value || strings.ContainsAny(value, "\\%?#") {
		return false
	}
	return strings.HasPrefix(value, "/v1/") ||
		strings.HasPrefix(value, "/v1beta/") ||
		strings.HasPrefix(value, "/backend-api/codex/") ||
		value == "/antigravity/models" ||
		strings.HasPrefix(value, "/antigravity/v1/") ||
		strings.HasPrefix(value, "/antigravity/v1beta/")
}

// Sub2API returns a root-relative URL in both Location and the accepted JSON.
func prefixSub2APIImagePollURL(response *http.Response, location string) error {
	const maxAcceptedBody = 32 << 10
	body, err := io.ReadAll(io.LimitReader(response.Body, maxAcceptedBody+1))
	if err != nil || len(body) > maxAcceptedBody {
		return errors.New("invalid Sub2API image task response")
	}
	var payload map[string]any
	if err := common.Unmarshal(body, &payload); err != nil || payload["poll_url"] != location {
		return errors.New("invalid Sub2API image poll URL")
	}
	payload["poll_url"] = sub2apiPrefix + location
	body, err = common.Marshal(payload)
	if err != nil {
		return err
	}
	_ = response.Body.Close()
	response.Body = io.NopCloser(bytes.NewReader(body))
	response.ContentLength = int64(len(body))
	response.Header.Set("Content-Length", strconv.Itoa(len(body)))
	return nil
}
