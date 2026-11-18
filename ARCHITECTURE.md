# Architecture

Pareme is one Go process. It stores a chain in three files, mines when the window gives it a hash, and talks to peers on TCP port 8080. The window is optional. The listener starts anyway.

`main` starts the log writer, opens the chain files, then starts the network manager and the miner. It then reads stdin. `ui` opens the Fyne window on that same goroutine, so the prompt waits until the window closes. `exit` cancels the process context and waits for the `WaitGroup`. Stop Node in the window does the same and then quits Fyne. The chain files close after that wait.

Mining starts when the window sends a hash. Dials start from Direct Connect, or from Connect to Network. Peer discovery and chain sync sit commented out in `main`. The window button is what calls them.

## Packages

| Path | Job |
| --- | --- |
| `src/main.go` | Starts the long-lived goroutines and the stdin loop |
| `src/common` | Block, peer, and message types, nBits conversion, the log |
| `src/inout` | Chain files, verification, difficulty |
| `src/mine` | Builds a candidate and searches nonces |
| `src/network` | TCP listener, dials, the message protocol |
| `src/ui` | Fyne window |

`main` imports the other five. `ui` and `network` import `common`. `mine` imports `common` and `inout`. `inout` imports `common` and `network`.

That last import is only `BroadcastBlock`. `network` cannot import `inout`, so it declares the `Chain` methods it calls. `main` passes the `*inout.Chain` through that seam. The checks in `verify.go` take blocks and do not open files.

## The chain module owns the files

`inout.Open` is the only opener of `blocks/`. The value it returns holds the three files, the in-memory orphans, and a mutex. Callers use three methods.

**Submit.** Verifies a batch and appends what passed. The kind string is `mined`, `received`, or `sync`. A file error comes back to the caller. A block that fails verification does not.

**Read.** One group of blocks per height. A failed read is logged and comes back empty.

**Stats.** Latest height and the number of stored blocks. A failed read is logged and comes back as zeros.

The mutex stays inside those methods. `verifyBlocks` and `CalculateDifficulty` never take it, and they never send on a channel. `Submit` loads the ancestor blocks, then calls `verifyBlocks`.

`Submit` keeps orphans for the next batch. It appends accepted blocks, updates the indexes, and broadcasts each accepted block in its own goroutine. If `MiningState.Active` is set, the accepted heights go to the miner on a channel created in `main` and passed into `Open` and `MinerManager`. That send runs on its own goroutine, because the miner may be the caller of `Submit`.

`sync` excludes every current peer from that broadcast. `mined` and `received` exclude the `From` peer. A mined block has a nil `From`, so it goes to every peer.

An empty verify returns with the mutex released.

## Block bytes

A block is 84 bytes, big-endian.

| Offset | Field | Size |
| --- | --- | --- |
| 0 | Height | 4 |
| 4 | Timestamp, Unix milliseconds | 8 |
| 12 | Previous block hash | 32 |
| 44 | NBits | 4 |
| 48 | Nonce | 4 |
| 52 | BodyHash | 32 |

`Hash` is SHA-256 of those 84 bytes. The body hash is part of the preimage. The miner sets it to the 32 bytes the window sent.

Genesis is fixed. Height 1, timestamp `1230940800000`, previous hash all zeros, nonce 0, `NBits` of `MaxTarget`, body hash all zeros. `MaxTarget` is 32 bytes starting `00 00 ff ff` and then zeros. No target is allowed above that.

On disk each record is the 4 bytes `PARE` plus the 84-byte block, 88 bytes in all. On the wire, block requests and broadcasts send `ToByte` alone. A batch is a stride of 84.

## Chain files

All three live under `blocks/`.

`pareme0000.dat` is append-only records.

`dir0000.idx` is one big-endian uint32 per height. The value is the exclusive end index into the offset file for that height. After genesis the file is 4 bytes and the value is 1.

`off0000.idx` is 5-byte big-endian byte offsets into the data file. After genesis it is five zero bytes, pointing at the first record.

Height 1 is a special case in the reader. It always means offset index 0, one block. For a later height `h`, the reader takes directory entry `h-2` as the start index and entry `h-1` as the end index, then reads that many 5-byte offsets.

A height can hold more than one block. Writing a block at height `h` increments the directory entry for `h` and every entry after it, and inserts the new byte offset into the offset file at that position, shifting the tail. The files can store forks beside each other.

Every block that passes verification is stored, including a second block at a height that already has one. When the miner needs a parent, it takes the first block returned for the current tip height.

An empty data file gets genesis, then the two index files are truncated and rewritten with the genesis entries above. `writeBlocks` returns before the index update when the block height is 1, so that first record does not go through `updateIndexFiles`. A non-empty data file stays as it is, indexes included.

`readChainStats` treats directory size divided by 4 as the tip height, and offset-file size divided by 5 as the block count.

## Checks before a write

`verifyBlocks` sorts the batch by height. Heights must be contiguous, and the same height may repeat. A lone height-1 block is accepted only when its hash equals the genesis hash.

Every other block has to pass, in order:

1. Its hash is strictly under the target decoded from its own `NBits`.
2. Its previous hash equals the hash of some block already marked verified in this check. Those include the blocks loaded from disk.
3. Its height is the parent's height plus one.
4. Its timestamp is at least the median of up to 11 ancestor timestamps, walked back by previous hash.
5. Its timestamp is at most now plus 2 minutes.

The chain module loads up to 11 heights before the first new height and passes that flat list into `verifyBlocks`. A block whose parent is missing from that set and from earlier in the batch goes to the orphan list. `Submit` retries that list on the next call. Orphans are dropped when the process exits.

## Difficulty

`NBits` is the compact target form. `NBitsToTarget` and `TargetToNBits` convert it. The first byte is the size. The next three are the mantissa.

The miner copies the parent's `NBits` onto the candidate. It recomputes them when the parent height is a positive multiple of 2016 and is not 2016 itself. The first retarget is the block after height 4032.

The miner asks the chain for `DifficultyHeights`, then calls `CalculateDifficulty` with those groups. The check divides the time across that span by `2016 * 10` seconds, and clamps the ratio to the range 0.25 through 4. The new target is the previous target times that ratio, then capped at `MaxTarget`. Verification checks that the hash meets the `NBits` stored on the block. A peer block is not checked against this recomputation.

## Mining

`MinerManager` returns a `chan string`. The window sends a 64-character hex string to start and an empty string to stop. That string is the body hash, reused each time the miner builds another candidate. The default text in the window is `78d031901879f64da61a41dd1e0c8a16d1ace1b9b61133db079413a816e765d2`.

One goroutine runs `findNonce`. It tries nonces from 1 to `MaxNonce` of 1e9, hashes the whole block, and checks the interrupt channel every 1000 attempts. A hit is submitted to the chain as `mined`. Nonce `-1` means the search was interrupted or exhausted.

`MiningState` is a plain struct in `common`, with `Active`, `IsFinding`, and `Height`. The miner and `Submit` both touch it with no extra lock.

While `Active` is set, accepted heights from `Submit` arrive on the heights channel. If the highest of those is more than `MiningState.Height + 2`, the current search is interrupted. If the miner is not currently searching, it builds a new candidate on top of that highest height. A mined block that fails verification produces no height notification, so the miner does not start another candidate on its own.

## Peers

`NetworkManager` listens on `:8080` as soon as it starts. Outbound connections start only when something sends on `DialIPChan`. Direct Connect sends the address typed in the window. A host with no port gets port 8080. The dial timeout is 5 seconds.

`AllPeers` is a plain map from a slot index to `*Peer`, capped at 100. The manager inserts peers and deletes a peer whose context is done. The window and `BroadcastBlock` read the map with no extra lock. When the manager is idle it still walks that map in a `default` branch with no sleep.

Each peer gets `handleIncoming`, `handleOutgoing`, and a read loop. The read loop frames TCP bytes into messages. Outgoing writes go through the peer's `SendChan`, buffered to 10. `handleOutgoing` registers a request that has a response channel before it writes. `handleIncoming` delivers the matching response by `Reference`.

A message is an 8-byte big-endian header plus payload.

| Offset | Field |
| --- | --- |
| 0 | Size, uint16, header plus payload |
| 2 | Kind, uint8. 0 request, 1 response |
| 3 | Command, uint8 |
| 4 | Reference, uint16 |
| 6 | PayloadSize, uint16 |
| 8 | Payload |

| Command | Meaning |
| --- | --- |
| 0 | Ping. The response is a pong with an empty payload |
| 1 | Latest height. The response is a uint32 |
| 2 | Block request. The payload is uint32 heights. The response is concatenated 84-byte blocks |
| 3 | Block broadcast. The payload is one 84-byte block. The handler writes it as type `received` and answers with a pong. A block whose hash is already stored at that height is dropped |
| 4 | Peer discovery. The request's first byte is how many peers to return. The response is 16-byte IPs |

`FindPeers` dials `170.75.161.198` when the map is empty. Otherwise, if fewer than 10 peers are outbound, it asks the first outbound peer for enough addresses to reach 10 and dials those.

`SyncToPeers` asks one peer for its height. If that peer is at least 10 blocks ahead, it requests the missing heights in chunks of 100 and writes each chunk as type `sync`.

`RequestMessage` blocks until the response arrives. `BroadcastBlock` uses it, so a broadcast waits on each peer in turn.

## Window

`RunUI` builds one Fyne window, 600 by 400.

Network controls are an IP field with Direct Connect, and Connect to Network, which runs `FindPeers` and then `SyncToPeers`. A sync error cancels the process context.

The peer list refreshes every 5 seconds from `AllPeers`. Ping and Get Latest Height send command 0 and command 1 to each checked peer. Both buttons stay disabled while the map is empty.

Mining controls are the hash field, Start Mining, and Stop Mining. Those send on the miner channel.

The label under the buttons is local UI text. The file log is separate.

## Stop and log

Cancellation is one `context.Context` from `main`. The log writer, network manager, block writer, and miner manager all exit on `ctx.Done` and call `WaitGroup.Done`. Per-peer contexts are children of that context. Closing a peer connection cancels that peer only.

`common.InitPrinter` truncates `common/pareme.log` on every start, then appends. `PrintToLog` sends on a channel buffered to 100 and drops the message if it cannot enqueue within a second. On shutdown the printer waits 2 seconds, drains what is left, and writes a final line.
