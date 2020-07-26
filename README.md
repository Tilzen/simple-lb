# Simple Load Balancer
A simple load balancer using the Round Robin strategy. Just to play a little.

## Usage

Args:
```
  --backends (string):
    - Backends to load balance, separated with commas.
  --port (int):
    - Port to serve (default 3030).
```

Example of use:
```console
hostname@user:~$ simplelb --backends=http://localhost:5000,http://localhost:5001,http://localhost:5002
```
