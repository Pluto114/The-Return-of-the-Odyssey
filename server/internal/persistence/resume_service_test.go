package persistence

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestResumeServiceOptionsValidation(t *testing.T) {
	valid := ResumeServiceOptions{Addr: "127.0.0.1:6379", TokenTTL: 30 * time.Second, OperationTimeout: time.Second}
	if err := valid.Validate(); err != nil {
		t.Fatal(err)
	}
	for _, mutate := range []func(*ResumeServiceOptions){
		func(options *ResumeServiceOptions) { options.Addr = "" },
		func(options *ResumeServiceOptions) { options.Addr = "missing-port" },
		func(options *ResumeServiceOptions) { options.Addr = "127.0.0.1:" },
		func(options *ResumeServiceOptions) { options.Addr = ":6379" },
		func(options *ResumeServiceOptions) { options.Addr = "127.0.0.1:not-a-port" },
		func(options *ResumeServiceOptions) { options.DB = -1 },
		func(options *ResumeServiceOptions) { options.TokenTTL = 0 },
		func(options *ResumeServiceOptions) { options.OperationTimeout = 0 },
	} {
		options := valid
		mutate(&options)
		if err := options.Validate(); !errors.Is(err, ErrInvalidResumeServiceOptions) {
			t.Errorf("options %+v error = %v", options, err)
		}
	}
}

func TestOpenResumeServiceClassifiesBackendFailureWithoutPassword(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := OpenResumeService(ctx, ResumeServiceOptions{
		Addr: "127.0.0.1:6379", Password: "do-not-log-this", TokenTTL: time.Minute, OperationTimeout: 50 * time.Millisecond,
	})
	if !errors.Is(err, ErrResumeBackend) {
		t.Fatalf("OpenResumeService error = %v, want %v", err, ErrResumeBackend)
	}
	if strings.Contains(err.Error(), "do-not-log-this") {
		t.Fatalf("backend error exposed password: %v", err)
	}
}
