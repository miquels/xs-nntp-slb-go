#! /usr/bin/make -f

CC		= gcc
CFLAGS		= -Wall -g -O2
LDFLAGS		=

DESTDIR		:=
SBINDIR		= /usr/sbin
LOGDIR		= /var/log/news
OLDDIR		= /var/log/news/OLD
INITDIR		= /etc/init.d
DEFAULTDIR	= /etc/default
LOGROTATEDIR	= /etc/logrotate.d

all:		xs-nntp-slb xs-nntp-slb-go

xs-nntp-slb:	xs-nntp-slb.o
		$(CC) $(CFLAGS) $^ -o $@ $(LDFLAGS)

xs-nntp-slb-go:	log.go main.go nntpqueue.go nntpsession.go util.go
		go build

install:
		install -d -m 755 $(DESTDIR)$(SBINDIR)
		install -m 755 xs-nntp-slb $(DESTDIR)$(SBINDIR)/
		install -m 755 xs-nntp-slb-go $(DESTDIR)$(SBINDIR)/

install-all:	install
		install -d -m 775 -o news -g news $(DESTDIR)$(LOGDIR)
		install -d -m 755 $(DESTDIR)$(INITDIR)
		install -d -m 755 $(DESTDIR)$(DEFAULTDIR)
		install -d -m 755 $(DESTDIR)$(LOGROTATEDIR)
		install -d -m 755 -o news -g news $(DESTDIR)$(OLDDIR)
		install -m 755 debian/xs-nntp-slb.init \
			$(DESTDIR)$(INITDIR)/xs-nntp-slb
		@if [ ! -f $(DESTDIR)$(DEFAULTDIR)/xs-nntp-slb ]; then \
			echo install -m 644 debian/xs-nntp-slb.default \
				$(DESTDIR)$(DEFAULTDIR)/xs-nntp-slb; \
			install -m 644 debian/xs-nntp-slb.default \
				$(DESTDIR)$(DEFAULTDIR)/xs-nntp-slb; \
		fi
		@if [ ! -f $(DESTDIR)$(LOGROTATEDIR)/xs-nntp-slb ]; then \
			echo install -m 644 xs-nntp-slb.logrotate \
				$(DESTDIR)$(LOGROTATEDIR)/xs-nntp-slb; \
			install -m 644 debian/xs-nntp-slb.logrotate \
				$(DESTDIR)$(LOGROTATEDIR)/xs-nntp-slb; \
		fi

clean:
		rm -f *.o xs-nntp-slb xs-nntp-slb-go

