#! /usr/bin/make -f

CFLAGS	= -Wall -O2
LDFLAGS	=

all: xs-nntp-slb xs-nntp-slb-go

clean:
	rm -f xs-nntp-slb xs-nntp-slb-go *.o

xs-nntp-slb: xs-nntp-slb.c
	$(CC) $(CFLAGS) -o xs-nntp-slb xs-nntp-slb.c

xs-nntp-slb-go:
	go build

.PHONY: xs-nntp-slb-go

