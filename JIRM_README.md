# Webwormhole server

Download git

```bash
git clone https://github.com/saljam/webwormhole.git
```

## Install TURN server

Install on different server then WebWormhole.

Install Docker

Run (change ):

```bash
docker run --name coturn -d --network=host instrumentisto/coturn -n --log-file=stdout --use-auth-secret --static-auth-secret=MY_SHARED_SECRET --fingerprint --no-cli  --realm=my.realm.org --external-ip='$(detect-external-ip)' --relay-ip='1.2.3.4' --listening-ip='1.2.3.4'

# Change:
#   --relay-ip and --listening-ip --> local/private IP of the server
#   --static-auth-secret --> generate strong secret
#   --external-ip --> server public IP of the server
```

Based on coturn, resources:

* <https://github.com/coturn/coturn/wiki/turnserver>
* <https://hub.docker.com/r/instrumentisto/coturn> 

## Install and configure WebWormhole server

### Manual configuration

#### Build

Enter Webwormhole cloned directory

```bash
docker build --tag NAME:VERSION .
```

#### Run

Run Webwormhole server

```bash

docker run -d -p 443:9000 -p 80:8000 --name CONTAINER_NAME NAME:VERION -https="0.0.0.0:9000" -http="0.0.0.0:8000" -hosts="my.example.com" -turn="turn:my.turn.server:3478" -turn-secret="MY_SHARED_SECRET"

# Change:
#   -hosts --> set domain names under which webwormohole will be accessible 
#   -turn --> public IP of the TURN server
#   -turn-secret --> secret used in TURN server

```
