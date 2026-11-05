package model

import (
	"fmt"
	"strings"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// UNIFYAPI-FORK: the platform log -- every layer a request can die at, in one
// table a super admin can query.
//
// # WHY THIS EXISTS
//
// `logs` only records requests that reached the relay. A request rejected
// earlier leaves no row at all: middleware/auth.go aborts with
// abortWithOpenAiMessage and never calls RecordErrorLog, and a connection that
// fails TLS never becomes a request in the first place. On 2026-11-04 a
// customer reported sending requests that got no response and we could neither
// confirm nor deny it -- 400 of theirs had in fact arrived and been answered
// 401, found only because one container happened not to have been recreated
// since 22 October.
//
// The sources all existed by then; what was missing was somewhere to read them
// together. ALB access and connection logs are gzip in S3, the console's own
// log is a file on the host. Answering "what happened to this customer" meant
// three tools and an AWS login. This table is the one place instead.
//
// Rows are ingested, never authored by a user, and are read ONLY by
// RoleRootUser. They carry customer source addresses in full, which is a
// deliberate product decision -- see the API layer for the access rule.
type PlatformEvent struct {
	Id        int   `json:"id"`
	CreatedAt int64 `json:"created_at" gorm:"index"`

	// Which layer saw it: auth, alb_access, alb_conn.
	Source string `json:"source" gorm:"type:varchar(24);index"`
	// What happened, stable enough to filter on: token_not_recognised,
	// tls_handshake_failed, http_rejected.
	Kind     string `json:"kind" gorm:"type:varchar(48);index"`
	Severity string `json:"severity" gorm:"type:varchar(16);index"`

	ClientIP string `json:"client_ip" gorm:"type:varchar(64);index"`
	// Eight characters of the key the caller presented. Enough to match against
	// tokens.key and say WHICH key they hold; never the whole secret.
	KeyPrefix string `json:"key_prefix" gorm:"type:varchar(48);index"`

	Method    string `json:"method" gorm:"type:varchar(10)"`
	Path      string `json:"path" gorm:"type:varchar(255)"`
	Status    int    `json:"status" gorm:"index"`
	UserAgent string `json:"user_agent" gorm:"type:varchar(255)"`

	// Populated from ALB connection logs. A failed handshake has a protocol and
	// a verify status and nothing else, which is exactly the case `logs` and the
	// access log can never show.
	TLSProtocol string `json:"tls_protocol" gorm:"type:varchar(24)"`
	TLSCipher   string `json:"tls_cipher" gorm:"type:varchar(64)"`

	RequestId string `json:"request_id" gorm:"type:varchar(64);index"`
	Detail    string `json:"detail" gorm:"type:text"`

	// Ingest is re-entrant: an S3 object may be read again after a crash, a
	// restart, or an overlapping schedule. This is the natural key of the
	// underlying event, so a replay collides instead of duplicating. Without it
	// the table silently doubles every time a poll is retried.
	Dedupe string `json:"-" gorm:"type:varchar(96);uniqueIndex"`
}

const (
	PlatformEventSourceAuth      = "auth"
	PlatformEventSourceALBAccess = "alb_access"
	PlatformEventSourceALBConn   = "alb_conn"

	PlatformEventKindTokenNotRecognised = "token_not_recognised"
	PlatformEventKindTLSHandshakeFailed = "tls_handshake_failed"
	PlatformEventKindHTTPRejected       = "http_rejected"
)

// RecordPlatformEvents writes a batch, skipping rows whose Dedupe is already
// present. Ingest failure must never take down what it is observing, so the
// error is returned for the caller to log rather than surfaced to a request.
func RecordPlatformEvents(events []PlatformEvent) error {
	if len(events) == 0 {
		return nil
	}
	if DB == nil {
		return fmt.Errorf("platform event store is not initialised")
	}
	return DB.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "dedupe"}},
		DoNothing: true,
	}).CreateInBatches(events, 200).Error
}

// RecordAuthRejection is called from the request path, so it must not block it.
// A rejected caller is already being answered; recording why is best effort.
func RecordAuthRejection(e PlatformEvent) {
	e.Source = PlatformEventSourceAuth
	e.Kind = PlatformEventKindTokenNotRecognised
	e.Severity = "warn"
	if e.CreatedAt == 0 {
		e.CreatedAt = time.Now().Unix()
	}
	// A request id is unique per request, so it is the natural key. Without one
	// (it is set by middleware and could in principle be absent) fall back to a
	// value that cannot collide with a real id.
	if e.Dedupe == "" {
		if e.RequestId != "" {
			e.Dedupe = "auth:" + e.RequestId
		} else {
			e.Dedupe = fmt.Sprintf("auth:%d:%s:%s", e.CreatedAt, e.ClientIP, e.KeyPrefix)
		}
	}
	go func() {
		// This runs on the request path of a caller who is already being
		// refused. An unrecovered panic in a goroutine takes down the whole
		// process, so a rejected request must not be able to kill the console
		// -- which it would, before this recover, any time DB were unset.
		defer func() {
			if r := recover(); r != nil {
				fmt.Printf("[platform-event] record panicked: %v\n", r)
			}
		}()
		if DB == nil {
			return
		}
		if err := RecordPlatformEvents([]PlatformEvent{e}); err != nil {
			// Deliberately only logged: see the comment above.
			fmt.Printf("[platform-event] record failed: %v\n", err)
		}
	}()
}

type PlatformEventQuery struct {
	Source    string
	Kind      string
	ClientIP  string
	KeyPrefix string
	Status    int
	StartAt   int64
	EndAt     int64
	Page      int
	PageSize  int
}

func (q PlatformEventQuery) apply(tx *gorm.DB) *gorm.DB {
	if q.Source != "" {
		tx = tx.Where("source = ?", q.Source)
	}
	if q.Kind != "" {
		tx = tx.Where("kind = ?", q.Kind)
	}
	if q.ClientIP != "" {
		tx = tx.Where("client_ip = ?", q.ClientIP)
	}
	if q.KeyPrefix != "" {
		tx = tx.Where("key_prefix LIKE ?", strings.TrimSpace(q.KeyPrefix)+"%")
	}
	if q.Status != 0 {
		tx = tx.Where("status = ?", q.Status)
	}
	if q.StartAt > 0 {
		tx = tx.Where("created_at >= ?", q.StartAt)
	}
	if q.EndAt > 0 {
		tx = tx.Where("created_at <= ?", q.EndAt)
	}
	return tx
}

func SearchPlatformEvents(q PlatformEventQuery) (events []PlatformEvent, total int64, err error) {
	tx := q.apply(DB.Model(&PlatformEvent{}))
	if err = tx.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	if q.PageSize <= 0 || q.PageSize > 200 {
		q.PageSize = 50
	}
	if q.Page < 1 {
		q.Page = 1
	}
	err = q.apply(DB.Model(&PlatformEvent{})).
		Order("created_at desc, id desc").
		Offset((q.Page - 1) * q.PageSize).
		Limit(q.PageSize).
		Find(&events).Error
	return events, total, err
}

// DeletePlatformEventsBefore bounds the table. The sources it mirrors expire at
// 30 days (the S3 buckets) and the host log is already 521MB unbounded, so this
// one is not allowed to become the third place that grows forever.
func DeletePlatformEventsBefore(cutoff int64) (int64, error) {
	tx := DB.Where("created_at < ?", cutoff).Delete(&PlatformEvent{})
	return tx.RowsAffected, tx.Error
}
