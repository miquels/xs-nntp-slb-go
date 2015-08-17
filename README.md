
xs-nntp-slb is a simplistic NNTP loadbalancer.
==============================================

It is mainly meant to be used as a frontend to a cluster of
NNTP transit servers, to spread out the load.

It can only do static loadbalancing; if a backend server goes
down, xs-nntp-slb stops working.

xs-nntp-slb consists of 2 parts: a daemon, written in C, that accepts
connections on a TCP port (usually 119, ofcourse) which spawns
and executes a worker process written in Go.

The worker process makes multiple outgoing connections,
one to each backend. xs-nntp-slb-go uses the XCLIENT command to
forward the IP address of the client to each backend.

At each supported NNTP command that takes a message-id as
an argument, the message-id is hashed to a 32-bits number N.
Then the command is forwarded to backend-server
number (N modulo number_of_servers) .

