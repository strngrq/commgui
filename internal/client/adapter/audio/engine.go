package audio

import (
	"context"
	"fmt"
	"strings"

	"github.com/strngrq/commgui/internal/client/port"
)

// Engine — реализация port.AudioEngine, диспетчер по audio mode.
type Engine struct{}

// NewEngine создаёт новый Engine.
func NewEngine() *Engine {
	return &Engine{}
}

// Build собирает pipeline для заданного режима.
func (e *Engine) Build(ctx context.Context, opts port.AudioOpts) (port.AudioPipeline, error) {
	parsed, err := parseMode(opts.Mode)
	if err != nil {
		return nil, err
	}
	switch parsed.kind {
	case modeNull:
		return newNullPipeline(), nil
	case modeTone:
		return newTonePipeline(opts)
	case modeFile:
		return newFilePipeline(parsed.path, opts)
	case modeReal:
		return newRealPipeline(ctx, opts)
	default:
		return nil, fmt.Errorf("unsupported audio mode %q", opts.Mode)
	}
}

const (
	modeNull = "null"
	modeTone = "tone"
	modeFile = "file"
	modeReal = "real"
)

type parsedMode struct {
	kind string
	path string
}

func parseMode(mode string) (parsedMode, error) {
	switch {
	case mode == "", mode == modeNull:
		return parsedMode{kind: modeNull}, nil
	case mode == modeTone:
		return parsedMode{kind: modeTone}, nil
	case mode == modeReal:
		return parsedMode{kind: modeReal}, nil
	case mode == modeFile:
		return parsedMode{kind: modeFile}, nil
	case strings.HasPrefix(mode, "file:"):
		path := strings.TrimPrefix(mode, "file:")
		return parsedMode{kind: modeFile, path: path}, nil
	default:
		return parsedMode{}, fmt.Errorf("unsupported audio mode %q", mode)
	}
}
