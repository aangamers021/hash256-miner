package orchestrator

import (
	"context"
	"encoding/hex"
	"fmt"
	"math/big"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"

	"github.com/hash256-miner/hash256-miner/internal/chain"
	"github.com/hash256-miner/hash256-miner/internal/config"
	"github.com/hash256-miner/hash256-miner/internal/logx"
	"github.com/hash256-miner/hash256-miner/internal/wallet"
)

type Engine struct {
	Cfg       *config.Config
	Reader    *chain.Reader
	Submitter *chain.Submitter
	Wallets   *wallet.Manager
	CoreBin   string

	workers   []*workerSlot
	workersMu sync.Mutex

	challenge     atomic.Value
	difficulty    atomic.Value
	epoch         atomic.Value
	gpuAvailable  bool
	gpuDeviceList []GPUDevice

	submittedMu  sync.Mutex
	submitted    map[string]time.Time
	hashrateAcc  atomic.Uint64
	hashesAcc    atomic.Uint64
}

type workerSlot struct {
	Account       *wallet.Account
	Worker        *Worker
	ActiveMode    WorkerMode
	LastStart     time.Time
	LastHashrate  atomic.Uint64
	LastHashes    atomic.Uint64
	LastElapsedMs atomic.Uint64
}

func NewEngine(
	cfg *config.Config,
	reader *chain.Reader,
	submitter *chain.Submitter,
	wallets *wallet.Manager,
	coreBin string,
) *Engine {
	return &Engine{
		Cfg:       cfg,
		Reader:    reader,
		Submitter: submitter,
		Wallets:   wallets,
		CoreBin:   coreBin,
		submitted: make(map[string]time.Time),
	}
}

func (e *Engine) Run(ctx context.Context) error {
	logx.Infof("engine starting: %d wallet(s), core=%s, mode=%s",
		e.Wallets.Count(), e.CoreBin, e.Cfg.Miner.Mode)

	if err := e.waitForMiningOpen(ctx); err != nil {
		return err
	}

	if err := e.pollState(ctx); err != nil {
		return fmt.Errorf("initial state: %w", err)
	}

	if err := e.spawnWorkers(ctx); err != nil {
		return fmt.Errorf("spawn workers: %w", err)
	}
	defer e.stopAllWorkers()

	pollTicker := time.NewTicker(time.Duration(e.Cfg.Mining.PollIntervalMs) * time.Millisecond)
	defer pollTicker.Stop()

	statsTicker := time.NewTicker(10 * time.Second)
	defer statsTicker.Stop()

	lastEpoch := e.currentEpoch()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-statsTicker.C:
			e.logHashrate()
		case <-pollTicker.C:
			if err := e.pollState(ctx); err != nil {
				logx.Warnf("poll state: %v", err)
				continue
			}
			ne := e.currentEpoch()
			if ne != nil && lastEpoch != nil && ne.Cmp(lastEpoch) != 0 {
				logx.Infof("epoch rotated: %s → %s, retargeting workers", lastEpoch, ne)
				e.retargetAll()
			}
			lastEpoch = ne
		}
	}
}

func (e *Engine) waitForMiningOpen(ctx context.Context) error {
	interval := time.Duration(e.Cfg.Mining.StandByPollIntervalMs) * time.Millisecond
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		gs, err := e.Reader.GenesisState(ctx)
		if err != nil {
			logx.Warnf("genesisState: %v", err)
		} else if gs.Complete {
			logx.Infof("genesis complete, mining should be open")
			return nil
		} else {
			logx.Infof("stand-by: genesis %s minted, %s remaining (~%s ETH raised)",
				gs.Minted, gs.Remaining, gs.EthRaised)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-t.C:
		}
	}
}

func (e *Engine) pollState(ctx context.Context) error {
	ms, err := e.Reader.MiningState(ctx)
	if err != nil {
		return err
	}
	e.difficulty.Store(ms.Difficulty)
	e.epoch.Store(ms.Epoch)

	if e.Wallets.Count() > 0 {
		addr := e.Wallets.Primary().Address
		ch, err := e.Reader.GetChallenge(ctx, addr)
		if err == nil {
			e.challenge.Store(ch)
		}
	}

	logx.Infof("state era=%s diff=%s minted=%s epoch=%s blocks_left=%s",
		ms.Era, shortDiff(ms.Difficulty), ms.Minted, ms.Epoch, ms.EpochBlocksLeft)
	return nil
}

func (e *Engine) currentEpoch() *big.Int {
	v := e.epoch.Load()
	if v == nil {
		return nil
	}
	return v.(*big.Int)
}

func (e *Engine) getDifficulty() *big.Int {
	v := e.difficulty.Load()
	if v == nil {
		return new(big.Int)
	}
	return v.(*big.Int)
}

func (e *Engine) getChallengeFor(ctx context.Context, addr common.Address) (string, error) {
	ch, err := e.Reader.GetChallenge(ctx, addr)
	if err != nil {
		return "", err
	}
	return challengeToHex32(ch), nil
}

func (e *Engine) spawnWorkers(ctx context.Context) error {
	e.workersMu.Lock()
	defer e.workersMu.Unlock()

	resolvedMode := e.resolveMode(ctx)
	logx.Infof("using worker mode: %s", resolvedMode)

	for i, acc := range e.Wallets.Accounts() {
		w := NewWorker(e.CoreBin)
		if err := w.Start(ctx); err != nil {
			return fmt.Errorf("start worker[%d]: %w", i, err)
		}
		e.workers = append(e.workers, &workerSlot{
			Account:    acc,
			Worker:     w,
			ActiveMode: resolvedMode,
		})
		go e.handleWorkerEvents(ctx, acc, w)

		ready := <-w.Events()
		if ready.Event != "ready" {
			return fmt.Errorf("worker[%d] unexpected first event: %s", i, ready.Event)
		}
		logx.Infof("worker[%d] %s ready v%s cpu=%d gpu=%d",
			i, shortAddr(acc.Address), ready.Version, ready.CPUThreads, len(ready.GPUDevices))
		if len(ready.GPUDevices) > 0 {
			e.gpuAvailable = true
			e.gpuDeviceList = ready.GPUDevices
		}

		if err := e.startJob(ctx, acc, w, resolvedMode); err != nil {
			return fmt.Errorf("start job[%d]: %w", i, err)
		}
	}
	return nil
}

func (e *Engine) resolveMode(ctx context.Context) WorkerMode {
	switch strings.ToLower(e.Cfg.Miner.Mode) {
	case "gpu":
		return ModeGPU
	case "cpu":
		return ModeCPU
	default:
		if e.Cfg.Miner.Mode == "auto" && e.detectGPU(ctx) {
			return ModeGPU
		}
		return ModeCPU
	}
}

func (e *Engine) detectGPU(ctx context.Context) bool {
	probeWorker := NewWorker(e.CoreBin)
	if err := probeWorker.Start(ctx); err != nil {
		return false
	}
	defer probeWorker.Stop()
	ready, ok := <-probeWorker.Events()
	if !ok {
		return false
	}
	return ready.Event == "ready" && len(ready.GPUDevices) > 0
}

func (e *Engine) startJob(ctx context.Context, acc *wallet.Account, w *Worker, mode WorkerMode) error {
	ch, err := e.getChallengeFor(ctx, acc.Address)
	if err != nil {
		return fmt.Errorf("challenge for %s: %w", shortAddr(acc.Address), err)
	}
	diff := difficultyToHex32(e.getDifficulty())
	prefix, err := randomPrefix24Hex()
	if err != nil {
		return err
	}
	cmd := StartCmd{
		Cmd:        "start",
		Challenge:  ch,
		Difficulty: diff,
		Prefix:     prefix,
		Batch:      e.Cfg.Miner.BatchSize,
		Mode:       string(mode),
	}
	if mode == ModeGPU && len(e.Cfg.Miner.GPUDevices) > 0 {
		idx := int(acc.Index) % len(e.Cfg.Miner.GPUDevices)
		d := e.Cfg.Miner.GPUDevices[idx]
		cmd.GPUDevice = &d
	}
	if mode == ModeCPU && e.Cfg.Miner.CPUThreads > 0 {
		t := e.Cfg.Miner.CPUThreads
		cmd.CPUThreads = &t
	}
	return w.Send(cmd)
}

func (e *Engine) retargetAll() {
	e.workersMu.Lock()
	defer e.workersMu.Unlock()
	diff := difficultyToHex32(e.getDifficulty())
	for _, s := range e.workers {
		ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
		ch, err := e.getChallengeFor(ctx, s.Account.Address)
		cancel()
		if err != nil {
			logx.Warnf("retarget: challenge fetch for %s failed: %v", shortAddr(s.Account.Address), err)
			continue
		}
		if err := s.Worker.Send(RetargetCmd{Cmd: "retarget", Challenge: ch, Difficulty: diff}); err != nil {
			logx.Warnf("retarget send %s: %v", shortAddr(s.Account.Address), err)
		}
	}
}

func (e *Engine) stopAllWorkers() {
	e.workersMu.Lock()
	defer e.workersMu.Unlock()
	for _, s := range e.workers {
		_ = s.Worker.Stop()
	}
	e.workers = nil
}

func (e *Engine) handleWorkerEvents(ctx context.Context, acc *wallet.Account, w *Worker) {
	for ev := range w.Events() {
		switch ev.Event {
		case "progress":
			e.hashrateAcc.Store(uint64(ev.Hashrate))
			e.hashesAcc.Store(ev.Hashes)
			e.updateWorkerStats(acc, ev.Hashrate, ev.Hashes, ev.ElapsedMs)
		case "found":
			logx.Infof("found[%s] hashes=%d elapsed=%dms nonce=%s",
				shortAddr(acc.Address), ev.Hashes, ev.ElapsedMs, shortHash(ev.Nonce))
			if err := e.onFound(ctx, acc, w, ev); err != nil {
				logx.Errorf("submit failed for %s: %v", shortAddr(acc.Address), err)
			}
		case "stopped":
			logx.Debugf("worker[%s] stopped after %d hashes", shortAddr(acc.Address), ev.Hashes)
		case "error":
			logx.Errorf("worker[%s] error: %s", shortAddr(acc.Address), ev.Message)
		}
	}
}

func (e *Engine) updateWorkerStats(acc *wallet.Account, hashrate float64, hashes uint64, elapsedMs uint64) {
	e.workersMu.Lock()
	defer e.workersMu.Unlock()
	for _, s := range e.workers {
		if s.Account == acc {
			s.LastHashrate.Store(uint64(hashrate))
			s.LastHashes.Store(hashes)
			s.LastElapsedMs.Store(elapsedMs)
			return
		}
	}
}

func (e *Engine) logHashrate() {
	e.workersMu.Lock()
	defer e.workersMu.Unlock()
	if len(e.workers) == 0 {
		return
	}
	var totalRate, totalHashes uint64
	diff := e.getDifficulty()
	for _, s := range e.workers {
		totalRate += s.LastHashrate.Load()
		totalHashes += s.LastHashes.Load()
	}
	rateStr := humanizeHashrate(float64(totalRate))
	etaStr := estimateETA(float64(totalRate), diff)
	if len(e.workers) == 1 {
		logx.Infof("hashrate=%s hashes=%s eta/hit~%s", rateStr, humanizeCount(totalHashes), etaStr)
	} else {
		logx.Infof("hashrate=%s (%d workers) hashes=%s eta/hit~%s",
			rateStr, len(e.workers), humanizeCount(totalHashes), etaStr)
	}
}

func humanizeHashrate(h float64) string {
	switch {
	case h >= 1e9:
		return fmt.Sprintf("%.2f GH/s", h/1e9)
	case h >= 1e6:
		return fmt.Sprintf("%.2f MH/s", h/1e6)
	case h >= 1e3:
		return fmt.Sprintf("%.2f kH/s", h/1e3)
	default:
		return fmt.Sprintf("%.0f H/s", h)
	}
}

func humanizeCount(n uint64) string {
	f := float64(n)
	switch {
	case f >= 1e9:
		return fmt.Sprintf("%.2fB", f/1e9)
	case f >= 1e6:
		return fmt.Sprintf("%.2fM", f/1e6)
	case f >= 1e3:
		return fmt.Sprintf("%.2fk", f/1e3)
	default:
		return fmt.Sprintf("%d", n)
	}
}

func estimateETA(hashrate float64, difficulty *big.Int) string {
	if hashrate <= 0 || difficulty == nil || difficulty.Sign() == 0 {
		return "—"
	}
	maxU := new(big.Int).Sub(new(big.Int).Lsh(big.NewInt(1), 256), big.NewInt(1))
	expected := new(big.Int).Div(maxU, difficulty)
	exp, _ := new(big.Float).SetInt(expected).Float64()
	secs := exp / hashrate
	switch {
	case secs < 60:
		return fmt.Sprintf("%.0fs", secs)
	case secs < 3600:
		return fmt.Sprintf("%.1fm", secs/60)
	case secs < 86400:
		return fmt.Sprintf("%.1fh", secs/3600)
	default:
		return fmt.Sprintf("%.1fd", secs/86400)
	}
}

func (e *Engine) onFound(ctx context.Context, acc *wallet.Account, w *Worker, ev WorkerEvent) error {
	nonceHex := strings.TrimPrefix(ev.Nonce, "0x")
	if len(nonceHex) != 64 {
		return fmt.Errorf("invalid nonce length %d", len(nonceHex))
	}
	nonceBytes, err := hex.DecodeString(nonceHex)
	if err != nil {
		return fmt.Errorf("decode nonce: %w", err)
	}
	nonce := new(big.Int).SetBytes(nonceBytes)

	if e.Cfg.Mining.VerifyBeforeSubmit {
		chV := e.challenge.Load()
		if chV == nil {
			return fmt.Errorf("no challenge cached")
		}
		ch := chV.([32]byte)
		if !verifyLocal(ch, nonceBytes, e.getDifficulty()) {
			return fmt.Errorf("local verify failed")
		}
	}

	e.submittedMu.Lock()
	submittedKey := strings.ToLower(acc.Address.Hex()) + ":" + ev.Nonce
	if _, already := e.submitted[submittedKey]; already {
		e.submittedMu.Unlock()
		return nil
	}
	e.submitted[submittedKey] = time.Now()
	e.submittedMu.Unlock()

	bumpFactor := 1.0
	if head, err := e.Reader.Client.BlockNumber(ctx); err == nil {
		mib, err2 := e.Reader.MintsInBlock(ctx, new(big.Int).SetUint64(head))
		if err2 == nil && mib != nil && mib.Int64() >= 7 {
			bumpFactor = e.Cfg.Mining.PriorityFeeBumpNearCap
			logx.Infof("block near cap (%d/10), bumping tip by %.1fx", mib.Int64(), bumpFactor)
		}
	}

	txHash, err := e.Submitter.SubmitMine(ctx, chain.SubmitParams{
		Key:        acc.PrivateKey,
		From:       acc.Address,
		TipCapGwei: e.Cfg.Mining.MaxPriorityFeeGwei,
		BumpFactor: bumpFactor,
	})
	if err != nil {
		return fmt.Errorf("broadcast: %w", err)
	}
	logx.Infof("submitted[%s] tx=%s", shortAddr(acc.Address), txHash.Hex())

	newMode := e.currentMode()
	if err := e.startJob(ctx, acc, w, newMode); err != nil {
		return fmt.Errorf("restart job: %w", err)
	}

	go e.watchTx(ctx, acc, txHash, nonce)
	return nil
}

func (e *Engine) watchTx(ctx context.Context, acc *wallet.Account, tx common.Hash, nonce *big.Int) {
	deadline := time.Now().Add(5 * time.Minute)
	t := time.NewTicker(6 * time.Second)
	defer t.Stop()
	for time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		r, err := e.Reader.Client.TransactionReceipt(ctx, tx)
		if err != nil {
			continue
		}
		if r.Status == 1 {
			logx.Infof("✓ mined[%s] nonce=%s tx=%s block=%d", shortAddr(acc.Address), nonce, tx.Hex(), r.BlockNumber)
		} else {
			logx.Warnf("✗ reverted[%s] tx=%s block=%d", shortAddr(acc.Address), tx.Hex(), r.BlockNumber)
		}
		return
	}
	logx.Warnf("tx timeout[%s] %s", shortAddr(acc.Address), tx.Hex())
}

func (e *Engine) currentMode() WorkerMode {
	return e.resolveMode(context.Background())
}

func verifyLocal(challenge [32]byte, nonce []byte, target *big.Int) bool {
	buf := make([]byte, 64)
	copy(buf[:32], challenge[:])
	copy(buf[32:], nonce)
	h := crypto.Keccak256(buf)
	got := new(big.Int).SetBytes(h)
	return got.Cmp(target) < 0
}

func shortAddr(a common.Address) string {
	s := a.Hex()
	if len(s) < 10 {
		return s
	}
	return s[:6] + "…" + s[len(s)-4:]
}

func shortHash(h string) string {
	if len(h) < 12 {
		return h
	}
	return h[:10] + "…" + h[len(h)-4:]
}

func shortDiff(v *big.Int) string {
	if v == nil {
		return "0"
	}
	h := v.Text(16)
	for len(h) < 64 {
		h = "0" + h
	}
	return "0x" + h[:8] + "…" + h[60:]
}

func (e *Engine) SubmittedKey(addr common.Address, nonce string) string {
	return strings.ToLower(addr.Hex()) + ":" + nonce
}
