package compiler

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"syscall"
	"testing"
	"time"
)

func TestRunProcessCancellationContracts(t *testing.T) {
	t.Run("before start", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		_, failure := runProcess(ctx, "missing", nil, "", false, 1024)
		if failure == nil || failure.operation != "Cancelled" || failure.message != "operation cancelled before child start" {
			t.Fatalf("pre-start cancellation = %+v", failure)
		}
	})

	t.Run("inherited pipes", func(t *testing.T) {
		program, err := os.Executable()
		if err != nil {
			t.Fatalf("locate test binary: %v", err)
		}
		marker := filepath.Join(t.TempDir(), "descendant.pid")
		arguments := []string{
			"-test.run=^TestProcessHelperProgram$",
			"slick-process-helper",
			"spawn=" + marker,
			"block",
		}
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan *processFailureData, 1)
		go func() {
			_, failure := runProcess(ctx, program, arguments, "", false, 1024)
			done <- failure
		}()

		var descendantPID int
		deadline := time.Now().Add(5 * time.Second)
		for time.Now().Before(deadline) {
			data, err := os.ReadFile(marker)
			if err == nil {
				descendantPID, err = strconv.Atoi(string(data))
				if err != nil {
					t.Fatalf("parse descendant PID: %v", err)
				}
				break
			}
			time.Sleep(10 * time.Millisecond)
		}
		if descendantPID == 0 {
			t.Fatal("descendant did not start")
		}
		t.Cleanup(func() {
			if process, err := os.FindProcess(descendantPID); err == nil {
				_ = process.Signal(syscall.SIGKILL)
			}
		})

		cancel()
		select {
		case failure := <-done:
			if failure == nil || failure.operation != "Cancelled" {
				t.Fatalf("in-flight cancellation = %+v", failure)
			}
		case <-time.After(time.Second):
			t.Fatal("process wait remained blocked by inherited pipes")
		}
	})
}
