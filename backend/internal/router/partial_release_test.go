package router_test

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"

	"github.com/gin-gonic/gin"
	"path/filepath"
	"sync"
	"testing"

	"github.com/blueship581/print-color-calibration-release/backend/internal/database"
	"github.com/blueship581/print-color-calibration-release/backend/internal/router"
)

type runShape struct {
	ID               uint   `json:"id"`
	Version          uint   `json:"version"`
	Status           string `json:"status"`
	PlannedQuantity  int64  `json:"plannedQuantity"`
	ReleasedQuantity int64  `json:"releasedQuantity"`
}

type releaseShape struct {
	ID        uint   `json:"id"`
	StartNo   int64  `json:"startNo"`
	EndNo     int64  `json:"endNo"`
	Quantity  int64  `json:"quantity"`
	PressUnit string `json:"pressUnit"`
	Actor     string `json:"actor"`
}

type releaseResultShape struct {
	Release           releaseShape `json:"release"`
	Run               runShape     `json:"run"`
	ReleasedQuantity  int64        `json:"releasedQuantity"`
	RemainingQuantity int64        `json:"remainingQuantity"`
}

// setupProofingRun creates a run with the given plan and drives it to proofing.
func setupProofingRun(t *testing.T, engine *gin.Engine, tokens map[string]string, code string, plan int64) runShape {
	t.Helper()
	payload := recordPayload(code, "分批放行测试批次")
	payload["plannedQuantity"] = plan
	status, body := perform(t, engine, http.MethodPost, "/api/runs", tokens["operator"], "run-create-"+code, payload)
	if status != http.StatusCreated {
		t.Fatalf("create run status = %d body=%s", status, body)
	}
	run := decodeData[runShape](t, body)
	path := "/api/runs/" + uintString(run.ID) + "/transition"
	for _, target := range []string{"printing", "proofing"} {
		status, body = perform(t, engine, http.MethodPost, path, tokens["operator"], "run-"+target+"-"+code,
			map[string]any{"status": target, "expectedVersion": run.Version, "reason": "推进到 " + target})
		if status != http.StatusOK {
			t.Fatalf("run transition %s status = %d body=%s", target, status, body)
		}
		run = decodeData[runShape](t, body)
	}
	return run
}

func postRelease(t *testing.T, engine *gin.Engine, token, requestID string, runID uint, quantity int64) (int, []byte) {
	t.Helper()
	return perform(t, engine, http.MethodPost, "/api/runs/"+uintString(runID)+"/releases", token, requestID,
		map[string]any{"quantity": quantity, "pressUnit": "PU-001", "note": "分批放行"})
}

func TestPartialReleaseFlow(t *testing.T) {
	cfg := testConfig(filepath.Join(t.TempDir(), "gb517-release.db"))
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	db, redisClient, err := database.Open(context.Background(), cfg, logger)
	if err != nil {
		t.Fatalf("open test database: %v", err)
	}
	engine := router.New(cfg, db, redisClient, logger)
	tokens := map[string]string{}
	for _, role := range []string{"viewer", "operator", "reviewer"} {
		tokens[role] = loginToken(t, engine, role)
	}

	run := setupProofingRun(t, engine, tokens, "PR-PART-001", 6)
	if run.PlannedQuantity != 6 || run.ReleasedQuantity != 0 {
		t.Fatalf("unexpected plan counters: %+v", run)
	}

	// RBAC：只有 reviewer 及以上可以登记分批放行。
	if status, _ := postRelease(t, engine, tokens["viewer"], "release-viewer", run.ID, 1); status != http.StatusForbidden {
		t.Fatalf("viewer release status = %d, want 403", status)
	}
	if status, _ := postRelease(t, engine, tokens["operator"], "release-operator", run.ID, 1); status != http.StatusForbidden {
		t.Fatalf("operator release status = %d, want 403", status)
	}

	// 第一笔：累计 2/6，批次保持校样中。
	status, body := postRelease(t, engine, tokens["reviewer"], "release-first", run.ID, 2)
	if status != http.StatusCreated {
		t.Fatalf("first release status = %d body=%s", status, body)
	}
	first := decodeData[releaseResultShape](t, body)
	if first.ReleasedQuantity != 2 || first.RemainingQuantity != 4 || first.Run.Status != "proofing" {
		t.Fatalf("unexpected first release result: %+v", first)
	}
	if first.Release.StartNo != 1 || first.Release.EndNo != 2 || first.Release.PressUnit != "PU-001" {
		t.Fatalf("unexpected first interval: %+v", first.Release)
	}

	// 超量：本次 5 > 剩余 4，必须 422 且累计不变。
	if status, body = postRelease(t, engine, tokens["reviewer"], "release-overflow", run.ID, 5); status != http.StatusUnprocessableEntity {
		t.Fatalf("overflow release status = %d body=%s", status, body)
	}
	_, body = perform(t, engine, http.MethodGet, "/api/runs/"+uintString(run.ID), tokens["reviewer"], "run-after-overflow", nil)
	afterOverflow := decodeData[runShape](t, body)
	if afterOverflow.ReleasedQuantity != 2 || afterOverflow.Status != "proofing" {
		t.Fatalf("cumulative changed after failed release: %+v", afterOverflow)
	}

	// 第二笔满数：累计 6/6，批次转为已放行。
	status, body = postRelease(t, engine, tokens["reviewer"], "release-final", run.ID, 4)
	if status != http.StatusCreated {
		t.Fatalf("final release status = %d body=%s", status, body)
	}
	final := decodeData[releaseResultShape](t, body)
	if final.ReleasedQuantity != 6 || final.RemainingQuantity != 0 || final.Run.Status != "released" {
		t.Fatalf("unexpected final release result: %+v", final)
	}
	if final.Release.StartNo != 3 || final.Release.EndNo != 6 {
		t.Fatalf("unexpected final interval: %+v", final.Release)
	}

	// 满数后不能再登记。
	if status, _ = postRelease(t, engine, tokens["reviewer"], "release-after-full", run.ID, 1); status != http.StatusUnprocessableEntity {
		t.Fatalf("post-full release status = %d, want 422", status)
	}

	// 放行记录列表：两笔、区间连续且不重叠。
	_, body = perform(t, engine, http.MethodGet, "/api/runs/"+uintString(run.ID)+"/releases", tokens["viewer"], "release-list", nil)
	releases := decodeData[[]releaseShape](t, body)
	if len(releases) != 2 || releases[0].StartNo != 1 || releases[0].EndNo != 2 || releases[1].StartNo != 3 || releases[1].EndNo != 6 {
		t.Fatalf("unexpected release list: %+v", releases)
	}
}

func TestPartialReleaseConcurrency(t *testing.T) {
	cfg := testConfig(filepath.Join(t.TempDir(), "gb517-release-race.db") + "?_pragma=busy_timeout(5000)")
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	db, redisClient, err := database.Open(context.Background(), cfg, logger)
	if err != nil {
		t.Fatalf("open test database: %v", err)
	}
	engine := router.New(cfg, db, redisClient, logger)
	tokens := map[string]string{}
	for _, role := range []string{"operator", "reviewer"} {
		tokens[role] = loginToken(t, engine, role)
	}
	run := setupProofingRun(t, engine, tokens, "PR-PART-RACE", 4)

	// 四人同时提交同一数量：区间由服务端按累计推导，并发下必然撞同一区间，
	// 只有抢到区间的事务能成功，其余失败且累计不变。
	const attempts = 4
	statuses := make([]int, attempts)
	var wg sync.WaitGroup
	for index := 0; index < attempts; index++ {
		wg.Add(1)
		go func(slot int) {
			defer wg.Done()
			status, _ := postRelease(t, engine, tokens["reviewer"], fmt.Sprintf("release-race-%d", slot), run.ID, 2)
			statuses[slot] = status
		}(index)
	}
	wg.Wait()

	succeeded := 0
	for _, status := range statuses {
		if status == http.StatusCreated {
			succeeded++
		}
	}
	if succeeded < 1 || succeeded > 2 {
		t.Fatalf("concurrent successes = %d, want 1..2 (statuses=%v)", succeeded, statuses)
	}

	_, body := perform(t, engine, http.MethodGet, "/api/runs/"+uintString(run.ID), tokens["reviewer"], "race-run-read", nil)
	finalRun := decodeData[runShape](t, body)
	_, body = perform(t, engine, http.MethodGet, "/api/runs/"+uintString(run.ID)+"/releases", tokens["reviewer"], "race-release-list", nil)
	releases := decodeData[[]releaseShape](t, body)

	// 累计必须恰好等于已保留记录的总和，且区间互不重叠。
	if len(releases) != succeeded {
		t.Fatalf("stored releases = %d, want %d (one record per kept submission)", len(releases), succeeded)
	}
	var sum int64
	seen := map[int64]bool{}
	for _, release := range releases {
		if seen[release.StartNo] {
			t.Fatalf("overlapping interval kept: %+v", releases)
		}
		seen[release.StartNo] = true
		sum += release.Quantity
	}
	if finalRun.ReleasedQuantity != sum {
		t.Fatalf("cumulative %d does not match kept records %d", finalRun.ReleasedQuantity, sum)
	}
}
