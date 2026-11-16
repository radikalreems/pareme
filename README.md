# Pareme

Pareme is a blockchain node. It stores a local chain, mines blocks, and can open a desktop window.

## How it works

A block is a header plus up to 10 hashes. The header holds the height, timestamp, previous block hash, difficulty, and nonce. The node hashes the whole block with SHA-256. Mining tries nonces until that hash is under the difficulty target.

Blocks live in files under `blocks/`. `pareme0000.dat` is the chain data. Two index files record where each height sits in that file. A height can hold more than one block.

Startup reads those files and writes a genesis block when the data file is empty. After that the node waits. You mine and connect to peers from the window. A new block is checked, written, and sent to peers.

Difficulty is retargeted every 2016 blocks, aiming for about 10 seconds per block.

The explorer reads the data file and counts each body hash. The API on port 8000 returns that count.

## Run

Go 1.24. The window uses [Fyne](https://fyne.io), so the build needs CGO and a C compiler.

From the repo root:

```
go run ./src
```

The node starts headless. Type `ui` to open the window, or `exit` to quit.

It writes `blocks/`, `explorer/db`, and `common/pareme.log` in the working directory.

## Network

Peers use port 8080. The node does not connect out until you use Connect in the window.

`GET http://localhost:8000/hash/{hash}` returns how often that hash appears in block bodies. The hash is 64 hex characters.

## Code

```
src/main.go      entry point
src/common       block format and logging
src/inout        chain files and verification
src/mine         mining
src/network      peers
src/explorer     hash counts and the API
src/ui           desktop window
```
