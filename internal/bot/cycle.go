// Package bot contains the cross-cutting betting cycle that ties together
// market fetching, strategy selection, and execution.
//
// NOTE: The full implementation of RunBettingCycle was never committed to
// this repository. This file is a minimal stub so the module compiles and
// `go mod tidy` resolves cleanly — it is NOT a working bet loop. Calling
// it at runtime returns an error; the binaries cmd/bot, cmd/manager, and
// cmd/test-flow will refuse to place bets until a real implementation is
// restored. cmd/candle-rush-bot does not depend on this package and is
// unaffected.
package bot

import (
	"context"
	"errors"

	"github.com/blinq-fi/blinq-mm-bot/internal/betting"
	"github.com/blinq-fi/blinq-mm-bot/internal/config"
	"github.com/blinq-fi/blinq-mm-bot/internal/markets"
	"github.com/blinq-fi/blinq-mm-bot/internal/wallet"
)

// ErrNotImplemented is returned by RunBettingCycle until the real
// cycle logic is restored to this package.
var ErrNotImplemented = errors.New("bot.RunBettingCycle: internal/bot implementation missing from repo — stub only")

// RunBettingCycle is a placeholder matching the signature the caller
// binaries expect. It always returns ErrNotImplemented.
func RunBettingCycle(
	_ context.Context,
	_ *config.Config,
	_ *wallet.Manager,
	_ *markets.Fetcher,
	_ *betting.Strategy,
	_ *betting.Executor,
	_ bool, // dryRun
	_ bool, // hedged
) error {
	return ErrNotImplemented
}
