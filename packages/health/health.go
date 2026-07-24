// Package health provides composable, transport-independent readiness checks.
package health

import (
	"context"
	"encoding/json"
	"net/http"
	"sort"
	"sync"
)

const (
	StatusOK     = "ok"
	StatusFailed = "failed"
)

// Checker reports the readiness of one named dependency.
type Checker interface {
	Name() string
	Check(context.Context) error
}

// Result is the stable, serializable outcome of one Checker.
type Result struct {
	Name   string `json:"name"`
	Status string `json:"status"`
	Error  string `json:"error,omitempty"`
}

// Report is the aggregate readiness result.
type Report struct {
	Status string   `json:"status"`
	Checks []Result `json:"checks"`
}

// Run executes all checks concurrently and returns results ordered by name.
func Run(ctx context.Context, checkers ...Checker) Report {
	report := Report{Status: StatusOK, Checks: make([]Result, len(checkers))}
	var wait sync.WaitGroup
	for i, checker := range checkers {
		wait.Add(1)
		go func() {
			defer wait.Done()
			result := Result{Status: StatusOK}
			if checker == nil {
				result.Name = "unknown"
				result.Status = StatusFailed
				result.Error = "health checker is nil"
				report.Checks[i] = result
				return
			}
			result.Name = checker.Name()
			if err := checker.Check(ctx); err != nil {
				result.Status = StatusFailed
				result.Error = err.Error()
			}
			report.Checks[i] = result
		}()
	}
	wait.Wait()

	sort.SliceStable(report.Checks, func(i, j int) bool {
		return report.Checks[i].Name < report.Checks[j].Name
	})
	for _, result := range report.Checks {
		if result.Status == StatusFailed {
			report.Status = StatusFailed
			break
		}
	}
	return report
}

// Handler returns a net/http readiness handler for the supplied checks.
func Handler(checkers ...Checker) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		report := Run(request.Context(), checkers...)
		status := http.StatusOK
		if report.Status == StatusFailed {
			status = http.StatusServiceUnavailable
		}
		writer.Header().Set("Content-Type", "application/json; charset=utf-8")
		writer.WriteHeader(status)
		_ = json.NewEncoder(writer).Encode(report)
	})
}
