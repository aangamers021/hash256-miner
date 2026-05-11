.PHONY: all build build-go build-rust clean test run-wallet-init run-mine lint

BIN_DIR := bin
GO_BIN  := $(BIN_DIR)/hash256-miner
RUST_BIN := $(BIN_DIR)/hash256-miner-core

GOFLAGS := -trimpath -ldflags="-s -w"
CARGO_FLAGS := --release

# GPU feature is on by default; disable with: make build GPU=0
GPU ?= 1
ifeq ($(GPU),1)
	CARGO_FEATURES := --features gpu
else
	CARGO_FEATURES :=
endif

all: build

build: build-rust build-go

build-go:
	@mkdir -p $(BIN_DIR)
	go build $(GOFLAGS) -o $(GO_BIN) ./cmd/hash256-miner

build-rust:
	@mkdir -p $(BIN_DIR)
	cd miner-core && cargo build $(CARGO_FLAGS) $(CARGO_FEATURES)
	cp miner-core/target/release/hash256-miner-core $(RUST_BIN)

clean:
	rm -rf $(BIN_DIR) dist
	cd miner-core && cargo clean

test:
	go test ./...
	cd miner-core && cargo test

lint:
	go vet ./...
	cd miner-core && cargo clippy -- -D warnings

# Convenience run targets
wallet-init: build-go
	$(GO_BIN) wallet init

mine: build
	$(GO_BIN) mine start --config configs/miner.toml
