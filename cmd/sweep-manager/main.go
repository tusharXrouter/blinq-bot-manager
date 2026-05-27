// sweep-manager runs a one-shot sweep of USDC and/or MON from every sub-wallet
// in the wallet store back to the owner wallet. Asks which token(s) to sweep
// at startup. For automation use --tokens both|usdc|mon plus --non-interactive.
package main

import (
	"context"
	"flag"
	"fmt"
	"math/big"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/ethclient"
	"github.com/joho/godotenv"

	"github.com/blinq-fi/blinq-mm-bot/internal/cli"
	"github.com/blinq-fi/blinq-mm-bot/internal/config"
	"github.com/blinq-fi/blinq-mm-bot/internal/contracts"
	"github.com/blinq-fi/blinq-mm-bot/internal/secret"
	"github.com/blinq-fi/blinq-mm-bot/internal/wallet"
)

func main() {
	_ = godotenv.Load()

	configPath := flag.String("config", "config.yaml", "Path to config file")
	tokensFlag := flag.String("tokens", "", "Which tokens to sweep: both|usdc|mon (empty = prompt)")
	nonInteractive := flag.Bool("non-interactive", false, "Skip prompts; require --tokens")
	flag.Parse()

	cfg, err := config.LoadManager(*configPath)
	if err != nil {
		fmt.Printf("  %s Load config: %v\n", cli.Error("✗"), err)
		os.Exit(1)
	}

	// Resolve which tokens to sweep
	sweepUSDC, sweepMON := resolveTokens(*tokensFlag, *nonInteractive)

	fmt.Println(cli.Banner("SWEEP MANAGER"))
	fmt.Printf("  Owner sweep target: from sub-wallets → owner\n")
	fmt.Printf("  Sweep USDC:        %s\n", boolFmt(sweepUSDC))
	fmt.Printf("  Sweep MON:         %s\n", boolFmt(sweepMON))
	fmt.Println()

	// Secrets — each resolved with the same priority:
	//   docker secret → ENV (from .env via godotenv, or parent shell) → prompt
	// The status lines below show which source actually populated each
	// value, so if you have OWNER_PRIVATE_KEY in .env you can confirm at a
	// glance that no prompt was about to happen for it.
	passphrase, passSrc := wallet.LoadPassphraseWithSource()
	if passphrase == "" {
		fmt.Printf("  %s KEYSTORE_PASSPHRASE is required to decrypt sub-wallet keys.\n", cli.Error("✗"))
		os.Exit(1)
	}
	ks := wallet.NewKeystore(passphrase)
	fmt.Printf("  Keystore:  passphrase from %s\n", passSrc)

	ownerKey, ownerSrc := resolveOwnerKey(cfg)
	if ownerKey == "" {
		fmt.Printf("  %s OWNER_PRIVATE_KEY is required.\n", cli.Error("✗"))
		os.Exit(1)
	}
	fmt.Printf("  Owner key: loaded from %s\n", ownerSrc)

	ownerWallet, err := wallet.NewWallet(ownerKey, cfg.RPCUrl, cfg.ChainID)
	if err != nil {
		fmt.Printf("  %s Create owner wallet: %v\n", cli.Error("✗"), err)
		os.Exit(1)
	}
	ownerKey = ""
	cfg.OwnerPrivateKey = ""

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigCh
		fmt.Printf("\n  %s Interrupted, finishing current wallet...\n", cli.Warning("!"))
		cancel()
	}()

	runSweep(ctx, cfg, ownerWallet, ks, sweepUSDC, sweepMON)
	fmt.Println(cli.Banner("SWEEP COMPLETE"))
}

// resolveTokens converts the --tokens flag (or an interactive prompt) into the
// (sweepUSDC, sweepMON) booleans used by runSweep.
func resolveTokens(tokensFlag string, nonInteractive bool) (bool, bool) {
	tokensFlag = strings.ToLower(strings.TrimSpace(tokensFlag))
	switch tokensFlag {
	case "both":
		return true, true
	case "usdc":
		return true, false
	case "mon":
		return false, true
	case "":
		// fall through to prompt or default
	default:
		fmt.Printf("  %s Unknown --tokens value %q (expected: both|usdc|mon)\n", cli.Error("✗"), tokensFlag)
		os.Exit(1)
	}

	if nonInteractive || !isInteractiveTerminal() {
		fmt.Printf("  %s --tokens is required in non-interactive mode\n", cli.Error("✗"))
		os.Exit(1)
	}

	choice := cli.Choose(
		"Which token(s) do you want to sweep back to the owner wallet?",
		[]string{
			"Both (USDC + MON)",
			"USDC only",
			"MON only",
		},
		0,
	)
	switch choice {
	case 0:
		return true, true
	case 1:
		return true, false
	case 2:
		return false, true
	}
	return true, true
}

// runSweep walks every wallet in the store and forwards USDC and/or MON to
// the owner. Funds gas from the owner when a sub-wallet has USDC but not
// enough MON to pay for the ERC-20 transfer.
func runSweep(ctx context.Context, cfg *config.Config, ownerWallet *wallet.Wallet, ks *wallet.Keystore, sweepUSDC, sweepMON bool) {
	sweepCtx, cancel := context.WithTimeout(ctx, 30*time.Minute)
	defer cancel()

	client, err := ethclient.Dial(cfg.RPCUrl)
	if err != nil {
		fmt.Printf("  %s Connect RPC: %v\n", cli.Error("✗"), err)
		return
	}
	defer client.Close()

	usdcAddr := common.HexToAddress(cfg.USDCAddress)
	usdc, err := contracts.NewERC20(usdcAddr, client)
	if err != nil {
		fmt.Printf("  %s Create USDC contract: %v\n", cli.Error("✗"), err)
		return
	}

	store, err := wallet.NewStore(cfg.Wallets.DBUrl, ks)
	if err != nil {
		fmt.Printf("  %s Open wallet store: %v\n", cli.Error("✗"), err)
		return
	}
	defer store.Close()

	wallets := store.GetAllWallets()
	fmt.Printf("  Scanning %d wallets...\n", len(wallets))

	minUSDCWei := wallet.ToBaseUnits(0.01, 6)
	minMONWei := wallet.ToWei(0.001)
	reserveGasWei := wallet.ToWei(0.001)

	gasPrice, _ := client.SuggestGasPrice(sweepCtx)
	nativeGasCost := new(big.Int).Mul(gasPrice, big.NewInt(21000))
	usdcGasCost := new(big.Int).Mul(gasPrice, big.NewInt(120000))

	var totalUSDC, totalMON float64
	var sweptUSDC, sweptMON, errs int

	for i, sw := range wallets {
		select {
		case <-sweepCtx.Done():
			fmt.Printf("  %s Interrupted\n", cli.Warning("!"))
			goto done
		default:
		}

		walletAddr := common.HexToAddress(sw.Address)
		prefix := fmt.Sprintf("  [%d/%d] %s", i+1, len(wallets), truncateAddr(sw.Address))

		var usdcBal, monBal *big.Int
		if sweepUSDC {
			usdcBal, err = usdc.BalanceOf(sweepCtx, walletAddr)
			if err != nil {
				errs++
				continue
			}
		} else {
			usdcBal = big.NewInt(0)
		}
		monBal, err = client.BalanceAt(sweepCtx, walletAddr, nil)
		if err != nil {
			errs++
			continue
		}

		hasUSDC := sweepUSDC && usdcBal.Cmp(minUSDCWei) > 0
		hasMON := sweepMON && monBal.Cmp(minMONWei) > 0
		if !hasUSDC && !hasMON {
			continue
		}

		usdcFloat := wallet.FromBaseUnits(usdcBal, 6)
		monFloat := wallet.FromWei(monBal)
		fmt.Printf("%s — %.4f USDC, %.4f MON\n", prefix, usdcFloat, monFloat)

		// Decrypt sub-wallet key
		privateKey := sw.PrivateKey
		if ks.Enabled() {
			privateKey, err = ks.Decrypt(privateKey)
			if err != nil {
				fmt.Printf("%s %s decrypt: %v\n", prefix, cli.Error("✗"), err)
				errs++
				continue
			}
		}
		w, err := wallet.NewWallet(privateKey, cfg.RPCUrl, cfg.ChainID)
		if err != nil {
			fmt.Printf("%s %s wallet: %v\n", prefix, cli.Error("✗"), err)
			errs++
			continue
		}

		// USDC sweep (may need gas top-up first)
		if hasUSDC {
			if monBal.Cmp(usdcGasCost) < 0 {
				txHash, err := ownerWallet.SendNative(sweepCtx, walletAddr, new(big.Int).Set(usdcGasCost))
				if err != nil {
					fmt.Printf("%s %s gas top-up: %v\n", prefix, cli.Error("✗"), err)
					errs++
					continue
				}
				receipt, err := ownerWallet.WaitForTx(sweepCtx, txHash)
				if err != nil || receipt.Status == 0 {
					fmt.Printf("%s %s gas top-up tx failed\n", prefix, cli.Error("✗"))
					errs++
					continue
				}
				time.Sleep(500 * time.Millisecond)
			}
			opts, err := w.GetTransactOpts(sweepCtx)
			if err != nil {
				fmt.Printf("%s %s opts: %v\n", prefix, cli.Error("✗"), err)
				errs++
				continue
			}
			tx, err := usdc.Transfer(sweepCtx, opts, ownerWallet.Address, usdcBal)
			if err != nil {
				fmt.Printf("%s %s USDC transfer: %v\n", prefix, cli.Error("✗"), err)
				errs++
				continue
			}
			receipt, err := w.WaitForTx(sweepCtx, tx.Hash())
			if err != nil || receipt.Status == 0 {
				fmt.Printf("%s %s USDC tx failed\n", prefix, cli.Error("✗"))
				errs++
				continue
			}
			fmt.Printf("%s %s swept %.4f USDC\n", prefix, cli.Success("✓"), usdcFloat)
			totalUSDC += usdcFloat
			sweptUSDC++
			time.Sleep(500 * time.Millisecond)
		}

		// MON sweep — re-fetch balance because USDC sweep just used some gas
		if !hasMON && !sweepMON {
			continue
		}
		currentMON, err := client.BalanceAt(sweepCtx, walletAddr, nil)
		if err != nil || currentMON.Cmp(minMONWei) <= 0 {
			continue
		}
		sweepable := new(big.Int).Sub(currentMON, nativeGasCost)
		sweepable.Sub(sweepable, reserveGasWei)
		if sweepable.Cmp(big.NewInt(0)) <= 0 {
			continue
		}
		txHash, err := w.SendNative(sweepCtx, ownerWallet.Address, sweepable)
		if err != nil {
			fmt.Printf("%s %s MON transfer: %v\n", prefix, cli.Error("✗"), err)
			errs++
			continue
		}
		receipt, err := w.WaitForTx(sweepCtx, txHash)
		if err != nil || receipt.Status == 0 {
			fmt.Printf("%s %s MON tx failed\n", prefix, cli.Error("✗"))
			errs++
			continue
		}
		monSwept := wallet.FromWei(sweepable)
		fmt.Printf("%s %s swept %.4f MON\n", prefix, cli.Success("✓"), monSwept)
		totalMON += monSwept
		sweptMON++
		time.Sleep(500 * time.Millisecond)
	}

done:
	fmt.Printf("\n  Summary: %d USDC wallets (%.4f USDC) | %d MON wallets (%.4f MON) | %d errors\n",
		sweptUSDC, totalUSDC, sweptMON, totalMON, errs)
}

func truncateAddr(addr string) string {
	if len(addr) > 12 {
		return addr[:6] + "..." + addr[len(addr)-4:]
	}
	return addr
}

func boolFmt(b bool) string {
	if b {
		return cli.Success("yes")
	}
	return cli.DimText("no")
}

func isInteractiveTerminal() bool {
	fi, err := os.Stdin.Stat()
	if err != nil {
		return false
	}
	return (fi.Mode() & os.ModeCharDevice) != 0
}

// resolveOwnerKey mirrors the helper in bet-bot-manager: prefer the value
// already expanded from config.yaml + .env, then docker secret / env /
// prompt. Treats the example-config placeholder as unset.
func resolveOwnerKey(cfg *config.Config) (string, secret.Source) {
	if k := cfg.OwnerPrivateKey; k != "" && k != "0xyourprivatekeyhere" {
		return k, secret.SourceEnv
	}
	return secret.LoadWithSource("owner_private_key", "OWNER_PRIVATE_KEY", "Enter owner wallet private key")
}
