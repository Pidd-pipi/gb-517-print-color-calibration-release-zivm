package router_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/blueship581/print-color-calibration-release/backend/internal/config"
	"github.com/blueship581/print-color-calibration-release/backend/internal/database"
	"github.com/blueship581/print-color-calibration-release/backend/internal/router"
	"github.com/gin-gonic/gin"
)

type apiEnvelope struct {
	Data json.RawMessage `json:"data"`
}

func TestRBACAndImmutableRevisionFlows(t *testing.T) {
	cfg := testConfig(filepath.Join(t.TempDir(), "gb517.db"))
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	db, redisClient, err := database.Open(context.Background(), cfg, logger)
	if err != nil {
		t.Fatalf("open test database: %v", err)
	}
	engine := router.New(cfg, db, redisClient, logger)
	tokens := map[string]string{}
	for _, role := range []string{"viewer", "operator", "reviewer", "admin"} {
		tokens[role] = loginToken(t, engine, role)
	}

	payload := recordPayload("RD-TEST-001", "测试放行决定")
	if status, _ := perform(t, engine, http.MethodPost, "/api/release", tokens["viewer"], "viewer-create", payload); status != http.StatusForbidden {
		t.Fatalf("viewer create status = %d, want 403", status)
	}
	status, body := perform(t, engine, http.MethodPost, "/api/release", tokens["operator"], "decision-create", payload)
	if status != http.StatusCreated {
		t.Fatalf("operator create decision status = %d body=%s", status, body)
	}
	decision := decodeData[struct {
		ID      uint `json:"id"`
		Version uint `json:"version"`
	}](t, body)
	transition := map[string]any{"status": "release", "expectedVersion": decision.Version, "reason": "quality gate accepted"}
	path := "/api/release/" + uintString(decision.ID) + "/transition"
	if status, _ := perform(t, engine, http.MethodPost, path, tokens["operator"], "operator-release", transition); status != http.StatusForbidden {
		t.Fatalf("operator release status = %d, want 403", status)
	}
	status, body = perform(t, engine, http.MethodPost, path, tokens["reviewer"], "reviewer-release", transition)
	if status != http.StatusOK {
		t.Fatalf("reviewer release status = %d body=%s", status, body)
	}
	status, body = perform(t, engine, http.MethodGet, "/api/release/"+uintString(decision.ID), tokens["reviewer"], "decision-read", nil)
	detail := decodeData[struct {
		Version   uint `json:"version"`
		Revisions []struct {
			Version   uint   `json:"version"`
			RequestID string `json:"requestId"`
		} `json:"revisions"`
	}](t, body)
	if status != http.StatusOK || detail.Version != 2 || len(detail.Revisions) != 2 || detail.Revisions[0].RequestID != "reviewer-release" {
		t.Fatalf("unexpected decision revision chain: status=%d detail=%+v", status, detail)
	}
	update := recordPayload("ignored", "不得覆盖的决定")
	update["expectedVersion"] = detail.Version
	if status, _ := perform(t, engine, http.MethodPut, "/api/release/"+uintString(decision.ID), tokens["operator"], "locked-update", update); status != http.StatusConflict {
		t.Fatalf("resolved decision update status = %d, want 409", status)
	}
	if status, _ := perform(t, engine, http.MethodDelete, "/api/release/"+uintString(decision.ID), tokens["admin"], "locked-delete", nil); status != http.StatusConflict {
		t.Fatalf("resolved decision delete status = %d, want 409", status)
	}

	runPayload := recordPayload("PR-TEST-001", "测试色彩配置")
	status, body = perform(t, engine, http.MethodPost, "/api/runs", tokens["operator"], "run-create", runPayload)
	run := decodeData[struct {
		ID      uint `json:"id"`
		Version uint `json:"version"`
	}](t, body)
	if status != http.StatusCreated {
		t.Fatalf("create run status = %d body=%s", status, body)
	}
	runPath := "/api/runs/" + uintString(run.ID) + "/transition"
	status, _ = perform(t, engine, http.MethodPost, runPath, tokens["operator"], "run-printing", map[string]any{"status": "printing", "expectedVersion": run.Version, "reason": "plates and ink verified"})
	if status != http.StatusOK {
		t.Fatalf("run transition status = %d", status)
	}
	if status, _ := perform(t, engine, http.MethodDelete, "/api/runs/"+uintString(run.ID), tokens["admin"], "locked-run-delete", nil); status != http.StatusConflict {
		t.Fatalf("active run delete status = %d, want 409", status)
	}
	_, body = perform(t, engine, http.MethodGet, "/api/runs/"+uintString(run.ID), tokens["operator"], "run-read", nil)
	runDetail := decodeData[struct {
		Revisions []struct {
			RequestID string `json:"requestId"`
		} `json:"revisions"`
	}](t, body)
	if len(runDetail.Revisions) != 2 || runDetail.Revisions[0].RequestID != "run-printing" {
		t.Fatalf("unexpected colour configuration revisions: %+v", runDetail.Revisions)
	}

	if status, _ := perform(t, engine, http.MethodGet, "/api/audits", tokens["viewer"], "viewer-audit", nil); status != http.StatusForbidden {
		t.Fatalf("viewer audit status = %d, want 403", status)
	}
	if status, _ := perform(t, engine, http.MethodGet, "/api/audits", tokens["reviewer"], "reviewer-audit", nil); status != http.StatusOK {
		t.Fatalf("reviewer audit status = %d, want 200", status)
	}
}

func TestPartialRunReleaseLedger(t *testing.T) {
	cfg := testConfig(filepath.Join(t.TempDir(), "gb517-release.db"))
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	db, redisClient, err := database.Open(context.Background(), cfg, logger)
	if err != nil {
		t.Fatalf("open test database: %v", err)
	}
	engine := router.New(cfg, db, redisClient, logger)
	tokens := map[string]string{}
	for _, role := range []string{"viewer", "operator", "reviewer", "admin"} {
		tokens[role] = loginToken(t, engine, role)
	}

	// Seeded PR-003 has 计划份数 6000 with two ledger entries totalling 3600, so
	// it stays 校样中 and shows 剩余 2400.
	status, body := perform(t, engine, http.MethodGet, "/api/runs?search=PR-003", tokens["reviewer"], "seeded-list", nil)
	var seeded []struct {
		ID              uint   `json:"id"`
		Status          string `json:"status"`
		PlannedCopies   int    `json:"plannedCopies"`
		ReleasedCopies  int    `json:"releasedCopies"`
		RemainingCopies int    `json:"remainingCopies"`
	}
	decodeEnvelope(t, body, &seeded)
	if status != http.StatusOK || len(seeded) != 1 {
		t.Fatalf("seeded run lookup status=%d payload=%s", status, body)
	}
	row := seeded[0]
	if row.Status != "proofing" || row.PlannedCopies != 6000 || row.ReleasedCopies != 3600 || row.RemainingCopies != 2400 {
		t.Fatalf("unexpected seeded ledger: %+v", row)
	}
	status, body = perform(t, engine, http.MethodGet, "/api/runs/"+uintString(row.ID), tokens["reviewer"], "seeded-detail", nil)
	seededDetail := decodeData[struct {
		Releases []struct {
			StartSequence   int    `json:"startSequence"`
			EndSequence     int    `json:"endSequence"`
			CompletedCopies int    `json:"completedCopies"`
			PressCode       string `json:"pressCode"`
		} `json:"releases"`
	}](t, body)
	if status != http.StatusOK || len(seededDetail.Releases) != 2 ||
		seededDetail.Releases[0].StartSequence != 1 || seededDetail.Releases[1].EndSequence != 3600 {
		t.Fatalf("seeded intervals overlap or are missing: %+v", seededDetail.Releases)
	}

	// A fresh batch used for the rest of the assertions.
	payload := recordPayload("PR-REL-001", "测试分批放行批次")
	payload["plannedCopies"] = 1000
	status, body = perform(t, engine, http.MethodPost, "/api/runs", tokens["operator"], "release-run-create", payload)
	if status != http.StatusCreated {
		t.Fatalf("create run status = %d body=%s", status, body)
	}
	run := decodeData[struct {
		ID      uint `json:"id"`
		Version uint `json:"version"`
	}](t, body)
	advance := func(requestID, target string, expected uint) {
		t.Helper()
		status, body = perform(t, engine, http.MethodPost, "/api/runs/"+uintString(run.ID)+"/transition", tokens["operator"], requestID,
			map[string]any{"status": target, "expectedVersion": expected, "reason": "moving batch toward proofing"})
		if status != http.StatusOK {
			t.Fatalf("transition to %s status = %d body=%s", target, status, body)
		}
		run.Version = expected + 1
	}
	advance("release-run-printing", "printing", run.Version)
	advance("release-run-proofing", "proofing", run.Version)

	// Direct 已放行 transition must be refused: release is quantity driven.
	status, body = perform(t, engine, http.MethodPost, "/api/runs/"+uintString(run.ID)+"/transition", tokens["reviewer"], "direct-release",
		map[string]any{"status": "released", "expectedVersion": run.Version, "reason": "trying one click full release"})
	if status != http.StatusUnprocessableEntity || !bytes.Contains(body, []byte("分批放行")) {
		t.Fatalf("direct release should be refused with reason, status=%d body=%s", status, body)
	}

	releasePath := "/api/runs/" + uintString(run.ID) + "/releases"
	first := map[string]any{"expectedVersion": run.Version, "completedCopies": 600, "pressCode": "PU-001", "reason": "首批 600 份完成"}

	// viewer and operator cannot release.
	if status, _ := perform(t, engine, http.MethodPost, releasePath, tokens["viewer"], "viewer-release", first); status != http.StatusForbidden {
		t.Fatalf("viewer partial release status = %d, want 403", status)
	}
	if status, _ := perform(t, engine, http.MethodPost, releasePath, tokens["operator"], "operator-release", first); status != http.StatusForbidden {
		t.Fatalf("operator partial release status = %d, want 403", status)
	}

	// Unknown 印刷机台 fails without mutating the ledger.
	unknownPress := map[string]any{"expectedVersion": run.Version, "completedCopies": 100, "pressCode": "NO-SUCH-PRESS", "reason": "bad press"}
	status, body = perform(t, engine, http.MethodPost, releasePath, tokens["reviewer"], "unknown-press", unknownPress)
	if status != http.StatusUnprocessableEntity || !bytes.Contains(body, []byte("印刷机台")) {
		t.Fatalf("unknown press should fail with visible reason, status=%d body=%s", status, body)
	}

	// First partial release: 600/1000, status stays 校样中, 剩余 400.
	status, body = perform(t, engine, http.MethodPost, releasePath, tokens["reviewer"], "first-partial-release", first)
	if status != http.StatusOK {
		t.Fatalf("first partial release status = %d body=%s", status, body)
	}
	afterFirst := decodeData[struct {
		Status          string `json:"status"`
		Version         uint   `json:"version"`
		PlannedCopies   int    `json:"plannedCopies"`
		ReleasedCopies  int    `json:"releasedCopies"`
		RemainingCopies int    `json:"remainingCopies"`
		Releases        []struct {
			StartSequence   int    `json:"startSequence"`
			EndSequence     int    `json:"endSequence"`
			CompletedCopies int    `json:"completedCopies"`
			PressCode       string `json:"pressCode"`
		} `json:"releases"`
	}](t, body)
	if afterFirst.Status != "proofing" || afterFirst.ReleasedCopies != 600 || afterFirst.RemainingCopies != 400 || afterFirst.Version != run.Version+1 {
		t.Fatalf("ledger after first release wrong: %+v", afterFirst)
	}
	if len(afterFirst.Releases) != 1 || afterFirst.Releases[0].StartSequence != 1 || afterFirst.Releases[0].EndSequence != 600 || afterFirst.Releases[0].PressCode != "PU-001" {
		t.Fatalf("first interval wrong: %+v", afterFirst.Releases)
	}
	run.Version = afterFirst.Version

	// Over-plan submission (600 + 500 > 1000) fails and the cumulative total is unchanged.
	over := map[string]any{"expectedVersion": run.Version, "completedCopies": 500, "pressCode": "PU-002", "reason": "too many copies"}
	status, body = perform(t, engine, http.MethodPost, releasePath, tokens["reviewer"], "over-plan-release", over)
	if status != http.StatusUnprocessableEntity || !bytes.Contains(body, []byte("超过计划")) {
		t.Fatalf("over-plan release should report reason, status=%d body=%s", status, body)
	}
	status, body = perform(t, engine, http.MethodGet, "/api/runs/"+uintString(run.ID), tokens["reviewer"], "after-over-plan", nil)
	unchanged := decodeData[struct {
		ReleasedCopies  int        `json:"releasedCopies"`
		RemainingCopies int        `json:"remainingCopies"`
		Releases        []struct{} `json:"releases"`
	}](t, body)
	if status != http.StatusOK || unchanged.ReleasedCopies != 600 || unchanged.RemainingCopies != 400 || len(unchanged.Releases) != 1 {
		t.Fatalf("failed release must not change cumulative quantity: %+v", unchanged)
	}

	// Two reviewers submit the same remaining interval concurrently with the
	// same optimistic version: exactly one succeeds, the loser gets 409, only
	// one ledger row exists and the batch flips to 已放行 at 1000/1000.
	duplicate := func(requestID string) map[string]any {
		return map[string]any{"expectedVersion": run.Version, "completedCopies": 400, "pressCode": "PU-002", "reason": "closing 400 copies " + requestID}
	}
	type callResult struct {
		status int
		body   []byte
	}
	results := make([]callResult, 2)
	done := make(chan struct{}, 2)
	start := make(chan struct{})
	for i, requestID := range []string{"concurrent-release-a", "concurrent-release-b"} {
		go func(index int, rid string) {
			<-start
			s, b := perform(t, engine, http.MethodPost, releasePath, tokens["reviewer"], rid, duplicate(rid))
			results[index] = callResult{s, b}
			done <- struct{}{}
		}(i, requestID)
	}
	close(start)
	for i := 0; i < 2; i++ {
		<-done
	}
	okCount, conflictCount := 0, 0
	for _, result := range results {
		switch result.status {
		case http.StatusOK:
			okCount++
		case http.StatusConflict:
			conflictCount++
			if !bytes.Contains(result.body, []byte("release_conflict")) {
				t.Fatalf("loser should receive release_conflict: %s", result.body)
			}
		default:
			t.Fatalf("concurrent release unexpected status %d body=%s", result.status, result.body)
		}
	}
	if okCount != 1 || conflictCount != 1 {
		t.Fatalf("concurrent submissions: ok=%d conflict=%d, want exactly one each", okCount, conflictCount)
	}

	status, body = perform(t, engine, http.MethodGet, "/api/runs/"+uintString(run.ID), tokens["reviewer"], "final-ledger", nil)
	final := decodeData[struct {
		Status          string `json:"status"`
		ReleasedCopies  int    `json:"releasedCopies"`
		RemainingCopies int    `json:"remainingCopies"`
		Releases        []struct {
			StartSequence   int `json:"startSequence"`
			EndSequence     int `json:"endSequence"`
			CompletedCopies int `json:"completedCopies"`
		} `json:"releases"`
	}](t, body)
	if status != http.StatusOK || final.Status != "released" || final.ReleasedCopies != 1000 || final.RemainingCopies != 0 || len(final.Releases) != 2 {
		t.Fatalf("final ledger wrong: %+v", final)
	}
	if final.Releases[1].StartSequence != 601 || final.Releases[1].EndSequence != 1000 {
		t.Fatalf("second interval overlaps the first: %+v", final.Releases)
	}

	// Once 满数, additional releases are rejected even with the latest version.
	status, body = perform(t, engine, http.MethodGet, "/api/runs/"+uintString(run.ID), tokens["reviewer"], "final-before-extra", nil)
	finalVersion := decodeData[struct {
		Version uint `json:"version"`
	}](t, body).Version
	extra := map[string]any{"expectedVersion": finalVersion, "completedCopies": 1, "pressCode": "PU-001", "reason": "late copies"}
	if status, body = perform(t, engine, http.MethodPost, releasePath, tokens["reviewer"], "post-full-release", extra); status != http.StatusUnprocessableEntity || !bytes.Contains(body, []byte("校样")) {
		t.Fatalf("release after full status = %d body=%s, want 422 with proofing reason", status, body)
	}
}

func decodeEnvelope[T any](t *testing.T, body []byte, target *T) {
	t.Helper()
	var envelope apiEnvelope
	if err := json.Unmarshal(body, &envelope); err != nil {
		t.Fatalf("decode envelope %s: %v", body, err)
	}
	if err := json.Unmarshal(envelope.Data, target); err != nil {
		t.Fatalf("decode data %s: %v", envelope.Data, err)
	}
}

func testConfig(dsn string) config.Config {
	return config.Config{
		AppName: "print-color-calibration-release", Environment: "test", Port: "0",
		DatabaseDriver: "sqlite", DatabaseDSN: dsn, JWTSecret: "gb517-router-tests-secret",
		TokenTTL: time.Hour, RequestLimit: 1000, StartupTimeout: time.Second,
		ShutdownTimeout: time.Second, ReadHeaderTimeout: time.Second, ReadTimeout: time.Second,
		WriteTimeout: time.Second, IdleTimeout: time.Second,
	}
}

func loginToken(t *testing.T, engine *gin.Engine, username string) string {
	t.Helper()
	status, body := perform(t, engine, http.MethodPost, "/api/auth/login", "", "login-"+username, map[string]any{"username": username, "password": "Admin123!"})
	if status != http.StatusOK {
		t.Fatalf("login %s status = %d body=%s", username, status, body)
	}
	return decodeData[struct {
		Token string `json:"token"`
	}](t, body).Token
}

func recordPayload(code, name string) map[string]any {
	return map[string]any{
		"code": code, "name": name, "description": "router integration test",
		"facility": "测试印刷区", "owner": "operator", "category": "校准",
		"riskLevel": "medium", "metricValue": 2.1, "metricUnit": "dE",
		"effectiveAt": time.Now().UTC().Format(time.RFC3339), "evidence": "spectrophotometer evidence", "relatedCode": "PR-001",
		"plannedCopies": 1000,
	}
}

func perform(t *testing.T, engine *gin.Engine, method, path, token, requestID string, payload any) (int, []byte) {
	t.Helper()
	var body io.Reader
	if payload != nil {
		encoded, err := json.Marshal(payload)
		if err != nil {
			t.Fatalf("marshal payload: %v", err)
		}
		body = bytes.NewReader(encoded)
	}
	request := httptest.NewRequest(method, path, body)
	request.Header.Set("X-Request-ID", requestID)
	if payload != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, request)
	return response.Code, response.Body.Bytes()
}

func decodeData[T any](t *testing.T, body []byte) T {
	t.Helper()
	var envelope apiEnvelope
	if err := json.Unmarshal(body, &envelope); err != nil {
		t.Fatalf("decode envelope %s: %v", body, err)
	}
	var value T
	if err := json.Unmarshal(envelope.Data, &value); err != nil {
		t.Fatalf("decode data %s: %v", envelope.Data, err)
	}
	return value
}

func uintString(value uint) string {
	return strconv.FormatUint(uint64(value), 10)
}
