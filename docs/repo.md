# NDNd Repo Daemon Usage Example

This guide shows how to run the repo daemon as the storage layer of a simple cloud-sync style deployment. The repo service stores named objects, the catalog daemon tracks metadata, and clients issue one-shot insert and delete commands.

Because the repo service runs on top of the NDN forwarder and router, we first configure the combined daemon with YaNFD (forwarding) and `ndnd-dv` (routing).

## Network Setup Example

### Configure the Combined Daemon

Use the following configuration on all nodes and save it as `conf.yml`. Replace `<node-name>` with the actual node name for each node, for example `server`, `catalog`, or `client`.

Full configuration examples with documentation for routing (`dv`) and forwarding (`fw`) can be found [here](../dv/dv.sample.yml) and [here](../fw/yanfd.sample.yml), respectively.

```yaml
dv:
  network: /testnet
  router: /testnet/<node-name>
  keychain: "insecure"
  neighbors:
    - uri: "udp://<ip_of_server>:6363" # server
    - uri: "udp://<ip_of_catalog>:6363" # catalog

fw:
  faces:
    udp:
      enabled_unicast: true
      enabled_multicast: false
      port_unicast: 6363
    tcp:
      enabled: true
      port_unicast: 6363
    websocket:
      enabled: false
  fw:
    threads: 2
```

1. The `network` name must be the same on all nodes in the network.
2. The `router` name must be unique for each node in the network.
3. The `faces` section configures the faces that the forwarder listens on. In this example, UDP and TCP faces are enabled on port 6363. The unix face is enabled by default and listens on `/run/nfd/nfd/sock`.
4. For this simple example, routing security is disabled with `insecure`, which is not recommended for production use.
5. To make the network converge more quickly, it is recommended to configure neighbors before starting the daemon. In a minimal setup, add the catalog and server as a neighbor on client node. You can also create routing neighbor relationships after the daemons are running, for example:

```sh
# if udp is blocked, use tcp instead
ndnd dv link-create "udp://<bob-ip>:6363"
```

Once the configuration file is in place, start the NDNd daemon on each node in the background:

```sh
# root permissions are required to bind to the default unix socket
sudo ndnd daemon conf.yml
```

## Trust Setup Example

Next, generate a trust anchor and per-node certificates. In the current implementation, repo security is based on packet signatures, certificate chains, and trust-schema validation. Receivers verify that a packet was signed by the claimed producer and that the signer's certificate is authorized to publish under the packet's namespace. This provides authenticity and namespace-based authorization.

First, generate the trust anchor key and certificate on a trusted node:

```sh
# generate an Ed25519 root key
ndnd sec keygen /ndnd ed25519 > root.key

# generate a self-signed certificate for the root key
ndnd sec sign-cert root.key < root.key > root.cert
```

Then generate keys and certificates for each node. In this example, all keys and certificates are generated on the same trusted node. 

```sh
# generate keys for <node-name>
ndnd sec keygen /ndnd/<node-name> ed25519 > <node-name>.key

# generate certificates for <node-name>
ndnd sec sign-cert root.key < <node-name>.key > <node-name>.cert
```

Store the generated key and certificate in the keychain directory used by the repo, catalog, or client process on that node.

For faster verification, all public certificates can also be pre-installed in each node's keychain or trust store so that signature validation does not depend on on-demand certificate fetching. Only public certificates should be replicated in this way; private keys must remain on their respective owners' nodes.

## Configure Repo, Catalog, and Client

Use the following configuration on all nodes and save it as `repo_conf.yml`

```yaml
repo:
  # [required] Name of the repo service
  name: /ndnd/<node-name>
  # [required] Repo storage directory
  storage_dir: /etc/ndn/repo/storage
  # [required] Keychain URI for security
  keychain: "<dir:///absolute/path/to/keychain>"
  # [required] List of full names of all trust anchors (root certificate names)
  trust_anchors:
    - "/ndnd/KEY/%0A%ECy%5C%03%8E%D6%18/NA/v=1773136734305"
  # [required] Catalog service name. For the catalog daemon, set this to its own service name.
  catalog_name: "/ndnd/catalog"
  # [optional] If true, skip certificate validity checks when consuming data (e.g. SVS snapshots)
  ignore_validity: false
```

## Start Services

Start the repo daemon:

```sh
ndnd repo run ./repo_conf.yml
```

Start the catalog daemon:

```sh
ndnd repo catalog ./repo_conf.yml
```

The client-side insert and delete commands are one-shot CLI operations and exit when complete.

### For insertion, the arguments are:

1. local file path
2. target repo prefix
3. config file
4. optional `-fileName` flag to specify the file name to use on the server

if `-fileName` is not specified, the original file name from the local file path is used

```sh
ndnd repo insert tmp/hello.txt /ndnd/server ./repo_conf.yml
```

For deletion, the arguments are:

1. file name
2. config file

```sh
ndnd repo delete hello.txt ./repo_conf.yml
```

