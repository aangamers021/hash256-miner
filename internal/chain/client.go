package chain

import (
	"context"
	"errors"
	"fmt"
	"math/big"
	"sync"
	"sync/atomic"
	"time"

	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/ethclient"
	"github.com/ethereum/go-ethereum/rpc"
)

type Client struct {
	endpoints []string
	chainID   *big.Int
	timeout   time.Duration

	mu     sync.Mutex
	idx    int32
	active *ethclient.Client
	rpcCli *rpc.Client
}

func NewClient(ctx context.Context, endpoints []string, chainID int64, timeout time.Duration) (*Client, error) {
	if len(endpoints) == 0 {
		return nil, fmt.Errorf("no RPC endpoints provided")
	}
	c := &Client{
		endpoints: endpoints,
		chainID:   big.NewInt(chainID),
		timeout:   timeout,
	}
	if err := c.connect(ctx); err != nil {
		return nil, err
	}
	return c, nil
}

func (c *Client) Close() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.active != nil {
		c.active.Close()
		c.active = nil
		c.rpcCli = nil
	}
}

func (c *Client) connect(ctx context.Context) error {
	var lastErr error
	start := atomic.LoadInt32(&c.idx)
	for i := 0; i < len(c.endpoints); i++ {
		idx := (int(start) + i) % len(c.endpoints)
		url := c.endpoints[idx]
		dialCtx, cancel := context.WithTimeout(ctx, c.timeout)
		r, err := rpc.DialContext(dialCtx, url)
		cancel()
		if err != nil {
			lastErr = fmt.Errorf("dial %s: %w", url, err)
			continue
		}
		ec := ethclient.NewClient(r)
		probeCtx, cancel2 := context.WithTimeout(ctx, c.timeout)
		id, err := ec.ChainID(probeCtx)
		cancel2()
		if err != nil {
			lastErr = fmt.Errorf("chainID %s: %w", url, err)
			r.Close()
			continue
		}
		if id.Cmp(c.chainID) != 0 {
			lastErr = fmt.Errorf("chainID mismatch on %s: expected %s, got %s", url, c.chainID, id)
			r.Close()
			continue
		}
		c.mu.Lock()
		if c.active != nil {
			c.active.Close()
		}
		c.active = ec
		c.rpcCli = r
		atomic.StoreInt32(&c.idx, int32(idx))
		c.mu.Unlock()
		return nil
	}
	return fmt.Errorf("no usable RPC endpoint: %w", lastErr)
}

func (c *Client) withClient(ctx context.Context, fn func(*ethclient.Client) error) error {
	c.mu.Lock()
	cli := c.active
	c.mu.Unlock()
	if cli == nil {
		if err := c.connect(ctx); err != nil {
			return err
		}
		c.mu.Lock()
		cli = c.active
		c.mu.Unlock()
	}
	err := fn(cli)
	if err != nil && shouldFailover(err) {
		atomic.AddInt32(&c.idx, 1)
		if rerr := c.connect(ctx); rerr != nil {
			return fmt.Errorf("primary failed (%v) and reconnect failed: %w", err, rerr)
		}
		c.mu.Lock()
		cli = c.active
		c.mu.Unlock()
		return fn(cli)
	}
	return err
}

func shouldFailover(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	return true
}

func (c *Client) CallContract(ctx context.Context, to common.Address, data []byte) ([]byte, error) {
	var out []byte
	callCtx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	err := c.withClient(callCtx, func(cli *ethclient.Client) error {
		msg := ethereum.CallMsg{To: &to, Data: data}
		res, err := cli.CallContract(callCtx, msg, nil)
		if err != nil {
			return err
		}
		out = res
		return nil
	})
	return out, err
}

func (c *Client) BlockNumber(ctx context.Context) (uint64, error) {
	var n uint64
	callCtx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	err := c.withClient(callCtx, func(cli *ethclient.Client) error {
		bn, err := cli.BlockNumber(callCtx)
		if err != nil {
			return err
		}
		n = bn
		return nil
	})
	return n, err
}

func (c *Client) BalanceOf(ctx context.Context, addr common.Address) (*big.Int, error) {
	var bal *big.Int
	callCtx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	err := c.withClient(callCtx, func(cli *ethclient.Client) error {
		b, err := cli.BalanceAt(callCtx, addr, nil)
		if err != nil {
			return err
		}
		bal = b
		return nil
	})
	return bal, err
}

func (c *Client) SuggestFees(ctx context.Context) (baseFee, tipCap *big.Int, err error) {
	callCtx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	err = c.withClient(callCtx, func(cli *ethclient.Client) error {
		head, herr := cli.HeaderByNumber(callCtx, nil)
		if herr != nil {
			return herr
		}
		baseFee = new(big.Int).Set(head.BaseFee)
		tip, terr := cli.SuggestGasTipCap(callCtx)
		if terr != nil {
			return terr
		}
		tipCap = tip
		return nil
	})
	return
}

func (c *Client) NonceAt(ctx context.Context, addr common.Address, pending bool) (uint64, error) {
	var n uint64
	callCtx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	err := c.withClient(callCtx, func(cli *ethclient.Client) error {
		var err error
		if pending {
			n, err = cli.PendingNonceAt(callCtx, addr)
		} else {
			n, err = cli.NonceAt(callCtx, addr, nil)
		}
		return err
	})
	return n, err
}

func (c *Client) EstimateGas(ctx context.Context, msg ethereum.CallMsg) (uint64, error) {
	var gas uint64
	callCtx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	err := c.withClient(callCtx, func(cli *ethclient.Client) error {
		g, err := cli.EstimateGas(callCtx, msg)
		if err != nil {
			return err
		}
		gas = g
		return nil
	})
	return gas, err
}

func (c *Client) SendTransaction(ctx context.Context, tx *types.Transaction) error {
	callCtx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	return c.withClient(callCtx, func(cli *ethclient.Client) error {
		return cli.SendTransaction(callCtx, tx)
	})
}

func (c *Client) TransactionReceipt(ctx context.Context, hash common.Hash) (*types.Receipt, error) {
	var r *types.Receipt
	callCtx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	err := c.withClient(callCtx, func(cli *ethclient.Client) error {
		receipt, err := cli.TransactionReceipt(callCtx, hash)
		if err != nil {
			return err
		}
		r = receipt
		return nil
	})
	return r, err
}

func (c *Client) ChainID() *big.Int { return new(big.Int).Set(c.chainID) }
