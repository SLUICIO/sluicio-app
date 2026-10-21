// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package api

import (
	"context"
	"crypto/subtle"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	imclickhouse "github.com/sluicio/sluicio-app/pkg/clickhouse"
	"github.com/sluicio/sluicio-app/pkg/httpserver"
	"github.com/sluicio/sluicio-app/services/cell-api/internal/deploymentlimits"
)

// refusedByEntityLimit answers a create that the monitored-entity limit
// refused, and reports whether it did. Every path that creates an
// integration or a system checks it first, so the refusal reads the same
// everywhere instead of as a failed save.
func (h *Handlers) refusedByEntityLimit(w http.ResponseWriter, err error) bool {
	if !deploymentlimits.IsEntityLimit(err) {
		return false
	}
	msg := "this instance has reached its limit of integrations and systems; remove one to add another"
	if h.MonitoredEntityLimit > 0 {
		msg = fmt.Sprintf("this instance allows at most %d integrations and systems combined; remove one to add another", h.MonitoredEntityLimit)
	}
	httpserver.WriteError(w, http.StatusConflict, msg)
	return true
}

// storageCache keeps the last storage measurement for a minute. The
// banner asks on every page load, by every user; the size moves slowly
// and the measurement is a query, so it is not taken per request.
type storageCache struct {
	at    time.Time
	usage imclickhouse.StorageUsage
	err   error
}

const storageCacheTTL = time.Minute

func (h *Handlers) storageUsage(ctx context.Context) (imclickhouse.StorageUsage, error) {
	h.storageMu.Lock()
	defer h.storageMu.Unlock()
	c := &h.storage
	if !c.at.IsZero() && time.Since(c.at) < storageCacheTTL {
		return c.usage, c.err
	}
	if h.ClickHouseConn == nil {
		return imclickhouse.StorageUsage{}, errors.New("no clickhouse connection")
	}
	mctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	c.usage, c.err = imclickhouse.MeasureStorage(mctx, h.ClickHouseConn)
	c.at = time.Now()
	return c.usage, c.err
}

// StorageStatus is the storage limit's state as the app shows it.
type StorageStatus struct {
	// LimitBytes is 0 when the deployment sets no limit; nothing else is
	// sent then.
	LimitBytes uint64 `json:"limit_bytes"`
	UsedBytes  uint64 `json:"used_bytes,omitempty"`
	// AtLimit is usage at or over the limit: telemetry is being refused.
	// It stays refused until usage falls below ResumesBelowBytes, which
	// is a little under the limit so the instance does not flap at the
	// edge; between the two it may still be refusing.
	AtLimit           bool   `json:"at_limit,omitempty"`
	ResumesBelowBytes uint64 `json:"resumes_below_bytes,omitempty"`
}

func (h *Handlers) storageStatus(ctx context.Context) (StorageStatus, error) {
	if h.StorageLimit.Bytes == 0 {
		return StorageStatus{}, nil
	}
	u, err := h.storageUsage(ctx)
	if err != nil {
		return StorageStatus{LimitBytes: h.StorageLimit.Bytes}, err
	}
	return StorageStatus{
		LimitBytes:        h.StorageLimit.Bytes,
		UsedBytes:         u.Bytes,
		AtLimit:           u.Bytes >= h.StorageLimit.Bytes,
		ResumesBelowBytes: uint64(float64(h.StorageLimit.Bytes) * imclickhouse.ResumeFraction),
	}, nil
}

// LimitsStatus is the deployment's limits as the app shows them. Each
// half is absent when the deployment sets no such limit.
type LimitsStatus struct {
	Storage           *StorageStatus `json:"storage,omitempty"`
	MonitoredEntities *struct {
		Used  int `json:"used"`
		Limit int `json:"limit"`
	} `json:"monitored_entities,omitempty"`
}

// getLimitsStatus: GET /api/v1/limits - the deployment's limits and how
// close the instance is to them, for the banner that explains why
// telemetry stopped arriving or why an integration cannot be added. Any
// signed-in member: whoever is looking at a page with no new data
// deserves the reason. Totals only, nothing per tenant.
func (h *Handlers) getLimitsStatus(w http.ResponseWriter, r *http.Request) {
	var out LimitsStatus
	if h.StorageLimit.Bytes > 0 {
		st, err := h.storageStatus(r.Context())
		if err != nil {
			// The limit is still worth knowing; the size is just unknown.
			h.Logger.Warn("limits status: storage measurement failed", "err", err)
		}
		out.Storage = &st
	}
	if h.MonitoredEntityLimit > 0 && h.Limits != nil {
		c, err := h.Limits.Count(r.Context())
		if err != nil {
			h.Logger.Warn("limits status: count failed", "err", err)
		} else {
			out.MonitoredEntities = &struct {
				Used  int `json:"used"`
				Limit int `json:"limit"`
			}{Used: c.Integrations + c.Systems, Limit: h.MonitoredEntityLimit}
		}
	}
	httpserver.WriteJSON(w, http.StatusOK, out)
}

// deploymentTokenOK reports whether the request carries the deployment's
// token (SLUICIO_DEPLOYMENT_TOKEN). Unset means no machine access, not
// open access. A separate secret from the health token on purpose: that
// response was designed to carry nothing per tenant, and this one names
// every organization.
func (h *Handlers) deploymentTokenOK(r *http.Request) bool {
	want := strings.TrimSpace(h.DeploymentToken)
	if want == "" {
		return false
	}
	got := strings.TrimSpace(strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "))
	if got == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(got), []byte(want)) == 1
}

// DeploymentUsage is the instance's usage against the limits its
// deployment sets, for whoever runs it to read.
type DeploymentUsage struct {
	MeasuredAt time.Time `json:"measured_at"`
	// MonitoredEntities is integrations and systems, the pair the
	// monitored-entity limit caps together. Limit is null when none is set.
	MonitoredEntities struct {
		Integrations int                         `json:"integrations"`
		Systems      int                         `json:"systems"`
		Total        int                         `json:"total"`
		Limit        *int                        `json:"limit"`
		ByOrg        []deploymentlimits.OrgCount `json:"by_org"`
	} `json:"monitored_entities"`
	// Storage is the telemetry database's size on disk. LimitBytes is
	// null when none is set; Error says why UsedBytes is missing.
	Storage struct {
		UsedBytes         *uint64           `json:"used_bytes"`
		LimitBytes        *uint64           `json:"limit_bytes"`
		AtLimit           bool              `json:"at_limit"`
		ResumesBelowBytes *uint64           `json:"resumes_below_bytes,omitempty"`
		ByTable           map[string]uint64 `json:"by_table,omitempty"`
		Error             string            `json:"error,omitempty"`
	} `json:"storage"`
}

// deploymentUsage: GET /api/v1/deployment/usage - integrations, systems
// and storage against the deployment's limits. For the deployment to
// poll (bearer SLUICIO_DEPLOYMENT_TOKEN), not for anyone signed in: what
// it does with the numbers - telling a customer they are near a limit -
// is its business. 404 when no token is set, so the endpoint does not
// exist on an instance that does not use it.
func (h *Handlers) deploymentUsage(w http.ResponseWriter, r *http.Request) {
	if strings.TrimSpace(h.DeploymentToken) == "" {
		httpserver.WriteError(w, http.StatusNotFound, "not found")
		return
	}
	if !h.deploymentTokenOK(r) {
		httpserver.WriteError(w, http.StatusUnauthorized, "deployment token required")
		return
	}
	var out DeploymentUsage
	out.MeasuredAt = time.Now().UTC()
	if h.Limits != nil {
		c, err := h.Limits.Count(r.Context())
		if err != nil {
			h.Logger.Error("deployment usage: count failed", "err", err)
			httpserver.WriteError(w, http.StatusInternalServerError, "count failed")
			return
		}
		out.MonitoredEntities.Integrations = c.Integrations
		out.MonitoredEntities.Systems = c.Systems
		out.MonitoredEntities.Total = c.Integrations + c.Systems
		out.MonitoredEntities.ByOrg = c.ByOrg
	}
	if h.MonitoredEntityLimit > 0 {
		lim := h.MonitoredEntityLimit
		out.MonitoredEntities.Limit = &lim
	}
	u, err := h.storageUsage(r.Context())
	if err != nil {
		out.Storage.Error = "storage could not be measured"
		h.Logger.Warn("deployment usage: storage measurement failed", "err", err)
	} else {
		used := u.Bytes
		out.Storage.UsedBytes = &used
		out.Storage.ByTable = u.ByTable
	}
	if h.StorageLimit.Bytes > 0 {
		lim := h.StorageLimit.Bytes
		resume := uint64(float64(lim) * imclickhouse.ResumeFraction)
		out.Storage.LimitBytes = &lim
		out.Storage.ResumesBelowBytes = &resume
		out.Storage.AtLimit = err == nil && u.Bytes >= lim
	}
	httpserver.WriteJSON(w, http.StatusOK, out)
}
