package cmdutil

import (
	"errors"
	"testing"

	"go.uber.org/zap"
)

func TestMustLogger_ReturnsNonNil(t *testing.T) {
	got := MustLogger()
	if got == nil {
		t.Fatal("MustLogger returned nil; expected a *zap.Logger")
	}
}

func TestMustLogger_LogsAtInfoLevel(t *testing.T) {
	logger := MustLogger()
	// Logger should not panic on basic Info calls. Real assertion: the
	// production-mode logger has Info level enabled by default.
	if !logger.Core().Enabled(zap.InfoLevel) {
		t.Errorf("MustLogger returned a logger with Info level disabled")
	}
}

func TestMustLogger_FactoryHookReceivesError(t *testing.T) {
	// When the underlying zap factory fails, MustLogger should call
	// fatal — substituting both the factory and the fatal handler lets
	// us verify behavior without actually log.Fatal'ing the test process.
	originalFactory := loggerFactory
	originalFatal := fatalHandler
	defer func() {
		loggerFactory = originalFactory
		fatalHandler = originalFatal
	}()

	wantErr := errors.New("synthetic factory failure")
	loggerFactory = func() (*zap.Logger, error) {
		return nil, wantErr
	}

	var fatalCalled bool
	var fatalErr error
	fatalHandler = func(err error) {
		fatalCalled = true
		fatalErr = err
	}

	_ = MustLogger()

	if !fatalCalled {
		t.Errorf("expected fatalHandler to be called when factory returned error")
	}
	if !errors.Is(fatalErr, wantErr) {
		t.Errorf("fatalHandler got %v, want wrapped %v", fatalErr, wantErr)
	}
}
