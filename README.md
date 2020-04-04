<div align="center"><img src ="https://i.imgur.com/lixyKZe.png" height="100px" /></div>

## Go Fusion

Official Golang implementation of the FUSION protocol.

FUSION would like to extend its gratitude to the Ethereum Foundation.  
FUSION has used the official open-source golang implementation of the Ethereum protocol.

## API Reference

The API reference can be found [here](https://github.com/FUSIONFoundation/efsn/wiki/FSN-RPC-API)

## Building the source

Building `efsn` requires both a Go (version 1.11 or later) and a C compiler.
You can install them using your favourite package manager.

On Ubuntu 18.04, run these commands to install dependencies:

```
add-apt-repository ppa:longsleep/golang-backports
apt-get update
apt-get install golang-go build-essential
```

Once the dependencies are installed, run

```shell
make efsn
```

or, to build the full suite of utilities:

```shell
make all
```

## Executables

The go-efsn project comes with several wrappers/executables found in the `cmd`
directory.

|    Command    | Description                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                          |
| :-----------: | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
|  **`efsn`**   | Our main FUSION CLI client. It is the entry point into the FUSION network (main-, test- or private net), capable of running as a full node (default), archive node (retaining all historical state) or a light node (retrieving data live). It can be used by other processes as a gateway into the FUSION network via JSON RPC endpoints exposed on top of HTTP, WebSocket and/or IPC transports. `efsn --help` for command line options.          |
|  `bootnode`   | Stripped down version of our FUSION client implementation that only takes part in the network node discovery protocol, but does not run any of the higher level application protocols. It can be used as a lightweight bootstrap node to aid in finding peers in private networks.                                                                                                                                                                                                                                                                 |
|   `rlpdump`   | Developer utility tool to convert binary RLP ([Recursive Length Prefix](https://github.com/ethereum/wiki/wiki/RLP)) dumps (data encoding used by the FUSION protocol both network as well as consensus wise) to user-friendlier hierarchical representation (e.g. `rlpdump --hex CE0183FFFFFFC4C304050583616263`).                                                                                                                                                                                                                                                                  |

## Running `efsn`

Going through all the possible command line flags is out of scope here (please  see `efsn --help`),but we've enumerated a few common parameter combos to get you up to speed quickly on how you can run your own `efsn` instance.

### Full node on the FUSION main network

By far the most common scenario is people wanting to simply interact with the FUSION
network: create accounts; transfer funds; deploy and interact with contracts. For this
particular use-case the user doesn't care about years-old historical data, so we can
fast-sync quickly to the current state of the network. To do so:

```shell
$ efsn console
```

This command will:
 * Start `efsn` in full sync mode (default, can be changed with the `--syncmode` flag),
   causing it to download more data in exchange for avoiding processing the entire history
   of the FUSION network, which is very CPU intensive.
 * Start up `efsn`'s built-in interactive,
   (via the trailing `console` subcommand) through which you can invoke all official [`web3` methods]
   as well as `efsn`'s own management APIs.
   This tool is optional and if you leave it out you can always attach to an already running
   `efsn` instance with `efsn attach`.

### Full node on the FUSION test network

Transitioning towards developers, if you'd like to play around with creating FUSION
contracts, you almost certainly would like to do that without any real money involved until
you get the hang of the entire system. In other words, instead of attaching to the main
network, you want to join the **test** network with your node, which is fully equivalent to
the main network, but with play-FSN only.

```shell
$ efsn --testnet console
```

The `console` subcommand has the exact same meaning as above and they are equally
useful on the testnet too. Please see above for their explanations if you've skipped here.

Specifying the `--testnet` flag, however, will reconfigure your `efsn` instance a bit:

 * Instead of using the default data directory (`~/.fusion` on Linux for example), `efsn`
   will nest itself one level deeper into a `testnet` subfolder (`~/.fusion/testnet` on
   Linux). Note, on OSX and Linux this also means that attaching to a running testnet node
   requires the use of a custom endpoint since `efsn attach` will try to attach to a
   production node endpoint by default. E.g.
   `efsn attach <datadir>/testnet/efsn.ipc`. Windows users are not affected by
   this.
 * Instead of connecting the main FUSION network, the client will connect to the test
   network, which uses different P2P bootnodes, different network IDs and genesis states.

*Note: Although there are some internal protective measures to prevent transactions from
crossing over between the main network and test network, you should make sure to always
use separate accounts for play-money and real-money. Unless you manually move
accounts, `efsn` will by default correctly separate the two networks and will not make any
accounts available between them.*

### Configuration

As an alternative to passing the numerous flags to the `efsn` binary, you can also pass a
configuration file via:

```shell
$ efsn --config /path/to/your_config.toml
```

To get an idea how the file should look like you can use the `dumpconfig` subcommand to
export your existing configuration:

```shell
$ efsn --your-favourite-flags dumpconfig
```

#### Docker quick start

One of the quickest ways to get FUSION up and running on your machine is by using
Docker:

```shell
docker run -d --name fusion -v /home/alice/fusion-node:/root \
           -p 9000:9000 -p 40408:40408 \
           jowenshaw/efsn
```

This will start `efsn` in full-sync mode with a DB memory allowance of 1GB just as the
above command does.  It will also create a persistent volume in your home directory for
saving your blockchain as well as map the default ports. There is also an `alpine` tag
available for a slim version of the image.

Do not forget `--rpcaddr 0.0.0.0`, if you want to access RPC from other containers
and/or hosts. By default, `efsn` binds to the local interface and RPC endpoints is not
accessible from the outside.

You can build your own image and run container from it (optional):

```shell
# build image
docker build -f Dockerfile -t myefsn .

# run container, you can specify your own <usual-flags> (see `efsn --help`)
docker run -d --name fusion -v /home/alice/fusion-node:/root \
           -p 9000:9000 -p 9001:9001 -p 40408:40408 \
           myefsn --rpc --rpcapi "web3,eth,net,fsn,fsntx" --rpcaddr 0.0.0.0 --rpccorsdomain "*"
```

### Programmatically interfacing `efsn` nodes

As a developer, sooner rather than later you'll want to start interacting with `efsn` and the
FUSION network via your own programs and not manually through the console. To aid
this, `efsn` has built-in support for a JSON-RPC based APIs ([standard APIs](https://github.com/ethereum/wiki/wiki/JSON-RPC)
and [`efsn` specific APIs](https://github.com/FUSIONFoundation/efsn/wiki/FSN-RPC-API)).
These can be exposed via HTTP, WebSockets and IPC (UNIX sockets on UNIX based
platforms, and named pipes on Windows).

The IPC interface is enabled by default and exposes all the APIs supported by `efsn`,
whereas the HTTP and WS interfaces need to manually be enabled and only expose a
subset of APIs due to security reasons. These can be turned on/off and configured as
you'd expect.

HTTP based JSON-RPC API options:

  * `--rpc` Enable the HTTP-RPC server
  * `--rpcaddr` HTTP-RPC server listening interface (default: `localhost`)
  * `--rpcport` HTTP-RPC server listening port (default: `9000`)
  * `--rpcapi` API's offered over the HTTP-RPC interface
  * `--rpccorsdomain` Comma separated list of domains from which to accept cross origin requests (browser enforced)
  * `--ws` Enable the WS-RPC server
  * `--wsaddr` WS-RPC server listening interface (default: `localhost`)
  * `--wsport` WS-RPC server listening port (default: `9001`)
  * `--wsapi` API's offered over the WS-RPC interface
  * `--wsorigins` Origins from which to accept websockets requests
  * `--ipcdisable` Disable the IPC-RPC server
  * `--ipcpath` Filename for IPC socket/pipe within the datadir (explicit paths escape it)

You'll need to use your own programming environments' capabilities (libraries, tools, etc) to
connect via HTTP, WS or IPC to a `efsn` node configured with the above flags and you'll
need to speak [JSON-RPC](https://www.jsonrpc.org/specification) on all transports. You
can reuse the same connection for multiple requests!

**Note: Please understand the security implications of opening up an HTTP/WS based
transport before doing so! Hackers on the internet are actively trying to subvert
FUSION nodes with exposed APIs! Further, all browser tabs can access locally
running web servers, so malicious web pages could try to subvert locally available
APIs!**

For security, `efsn` forbid unlock account when account-related RPCs are exposed by http.
If you still want to do this, please add the following option to start up `efsn`:

```shell
--allow-insecure-unlock             Allow insecure account unlocking when account-related RPCs are exposed by http
```

### Operating a private network

For devnet private network, please refer [Deploy private chain](https://github.com/FUSIONFoundation/efsn/wiki/Deploy-private-chain)

Maintaining your own private network is more involved as a lot of configurations taken for
granted in the official networks need to be manually set up.

#### Defining the private genesis state

First, you'll need to create the genesis state of your networks, which all nodes need to be
aware of and agree upon. This consists of a small JSON file (e.g. call it `genesis.json`):

```json
{
  "config": {
    "chainId": <arbitrary positive integer>,
    "homesteadBlock": 0,
    "eip150Block": 0,
    "eip155Block": 0,
    "eip158Block": 0,
    "byzantiumBlock": 0,
    "constantinopleBlock": 0,
    "petersburgBlock": 0
  },
  "alloc": {},
  "coinbase": "0x0000000000000000000000000000000000000000",
  "difficulty": "0x20000",
  "extraData": "",
  "gasLimit": "0x2fefd8",
  "nonce": "0x0000000000000042",
  "mixhash": "0x0000000000000000000000000000000000000000000000000000000000000000",
  "parentHash": "0x0000000000000000000000000000000000000000000000000000000000000000",
  "timestamp": "0x00"
}
```

The above fields should be fine for most purposes, although we'd recommend changing
the `nonce` to some random value so you prevent unknown remote nodes from being able
to connect to you. If you'd like to pre-fund some accounts for easier testing, create
the accounts and populate the `alloc` field with their addresses.

```json
"alloc": {
  "0x0000000000000000000000000000000000000001": {
    "balance": "111111111"
  },
  "0x0000000000000000000000000000000000000002": {
    "balance": "222222222"
  }
}
```

With the genesis state defined in the above JSON file, you'll need to initialize **every**
`efsn` node with it prior to starting it up to ensure all blockchain parameters are correctly
set:

```shell
$ efsn init path/to/genesis.json
```

#### Creating the rendezvous point

With all nodes that you want to run initialized to the desired genesis state, you'll need to
start a bootstrap node that others can use to find each other in your network and/or over
the internet. The clean way is to configure and run a dedicated bootnode:

```shell
$ bootnode --genkey=boot.key
$ bootnode --nodekey=boot.key
```

With the bootnode online, it will display an [`enode` URL](https://github.com/ethereum/wiki/wiki/enode-url-format)
that other nodes can use to connect to it and exchange peer information. Make sure to
replace the displayed IP address information (most probably `[::]`) with your externally
accessible IP to get the actual `enode` URL.

*Note: You could also use a full-fledged `efsn` node as a bootnode, but it's the less
recommended way.*

#### Starting up your member nodes

With the bootnode operational and externally reachable (you can try
`telnet <ip> <port>` to ensure it's indeed reachable), start every subsequent `efsn`
node pointed to the bootnode for peer discovery via the `--bootnodes` flag. It will
probably also be desirable to keep the data directory of your private network separated, so
do also specify a custom `--datadir` flag.

```shell
$ efsn --datadir=path/to/custom/data/folder --bootnodes=<bootnode-enode-url-from-above>
```

*Note: Since your network will be completely cut off from the main and test networks, you'll
also need to configure a miner to process transactions and create new blocks for you.*

#### Running a private miner

To start a `efsn` instance for mining, run it with all your usual flags, extended
by:

```shell
$ efsn <usual-flags> --mine --miner.threads=1 --etherbase=0x0000000000000000000000000000000000000000
```

Which will start mining blocks and transactions on a single CPU thread, crediting all
proceedings to the account specified by `--etherbase`. You can further tune the mining
by changing the default gas limit blocks converge to (`--targetgaslimit`) and the price
transactions are accepted at (`--gasprice`).

## Contribution

Thank you for considering to help out with the source code! We welcome contributions
from anyone on the internet, and are grateful for even the smallest of fixes!

If you'd like to contribute to go-efsn, please fork, fix, commit and send a pull request
for the maintainers to review and merge into the main code base.

Please make sure your contributions adhere to our coding guidelines:

 * Code must adhere to the official Go [formatting](https://golang.org/doc/effective_go.html#formatting)
   guidelines (i.e. uses [gofmt](https://golang.org/cmd/gofmt/)).
 * Code must be documented adhering to the official Go [commentary](https://golang.org/doc/effective_go.html#commentary)
   guidelines.
 * Pull requests need to be based on and opened against the `master` branch.
 * Commit messages should be prefixed with the package(s) they modify.
   * E.g. "eth, rpc: make trace configs optional"

Please see the [Developers' Guide](https://github.com/ethereum/go-ethereum/wiki/Developers'-Guide)
for more details on configuring your environment, managing project dependencies, and
testing procedures.

## License

The go-efsn library (i.e. all code outside of the `cmd` directory) is licensed under the
[GNU Lesser General Public License v3.0](https://www.gnu.org/licenses/lgpl-3.0.en.html),
also included in our repository in the `COPYING.LESSER` file.

The go-efsn binaries (i.e. all code inside of the `cmd` directory) is licensed under the
[GNU General Public License v3.0](https://www.gnu.org/licenses/gpl-3.0.en.html), also
included in our repository in the `COPYING` file.
