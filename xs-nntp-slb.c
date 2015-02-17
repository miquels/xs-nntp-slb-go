// +build ignore

/*
 * xs-nntp-slb	NNTP listener- forks off xs-nntp-slb-go command
 *		for each incoming connection.
 *
 */

#define _XOPEN_SOURCE 600
#define _GNU_SOURCE

#include <sys/types.h>
#include <sys/socket.h>
#include <netinet/in.h>
#include <netinet/tcp.h>
#include <sys/poll.h>
#include <netdb.h>
#include <syslog.h>
#include <stdarg.h>
#include <errno.h>
#include <stdio.h>
#include <stdlib.h>
#include <unistd.h>
#include <fcntl.h>
#include <signal.h>
#include <string.h>

#define MAXLISTEN	16

int do_syslog = 1;
int do_stderr = 1;
int do_debug = 0;

char *progname;

void do_log(int dbg, char *fmt, ...);

#define notice(fmt, x...) \
	do_log(0, fmt, ##x)

#define perrordie(fmt, x...) \
	do { \
		do_log(1, fmt, ##x); \
		exit(1); \
	} while(0)

#define debug(fmt, x...) \
	do_log(2, fmt, ##x)

void do_log(int mode, char *fmt, ...)
{
	char		buf[1024];
	char		*p;
	va_list		ap;

	if (mode == 2 && !do_debug)
		return;

	va_start(ap, fmt);
	vsnprintf(buf, sizeof(buf), fmt, ap);
	va_end(ap);

	p = strstr(buf, "\r\n");
	if (p) *p = 0;

	if (mode == 2) {
		fprintf(stderr, "DBG: %s\n", buf);
		return;
	}

	if (do_syslog)
		syslog(LOG_NOTICE, "%s", buf);

	if (do_stderr) {
		if (mode == 1)
			fprintf(stderr, "%s: ", progname);
		fprintf(stderr, "%s\n", buf);
	}
}

int do_poll(int fd, int events, int timeout)
{
	struct pollfd	pfd;
	int		r;

	pfd.fd = fd;
	pfd.events = events;
	r = poll(&pfd, 1, timeout * 1000);
	if (r == 0) {
		r = -1;
		errno = ETIMEDOUT;
	}

	return r;
}

int blocking(int fd, int block)
{
	int fl = fcntl(fd, F_GETFL);
	if (fl < 0)
		return -1;
	if (block)
		fl &= ~(O_NONBLOCK);
	else
		fl |= O_NONBLOCK;
	return fcntl(fd, F_SETFL, fl);
}

void parse_host_port(char *data, int passive,
			char **hostp, char **portp, char *buf, int bufsz)
{
	char *host = NULL;
	char *port = NULL;
	char *colon = NULL;
	char *h;

	*hostp = NULL;
	*portp = NULL;

	strncpy(buf, data, bufsz);
	buf[bufsz - 1] = 0;
	data = buf;

	if (*data == '[' && (h = strchr(data, ']')) != NULL) {
		data++;
		*h = 0;
		colon = (h[1] == ':') ? h + 1 : NULL;
	} else
		colon = strrchr(data, ':');

	if (colon) {
		host = data;
		*colon++ = 0;
		port = colon;
	} else {
		int i;
		for (i = 0; i < strlen(data); i++)
			if (data[i] < '0' || data[i] > '9')
				break;
		if (data[i] == 0)
			port = data;
		else
			host = data;
	}

	if (host && strcmp(host, "*") == 0)
		host = "0.0.0.0";

	*hostp = host;
	*portp = port;
}

int resolve(char *hostport, char *dflport, int stream, int passive,
		struct addrinfo **ai, int *nai, int naimax)
{
	struct addrinfo	hints, *res;
	memset(&hints, 0, sizeof(hints));
	hints.ai_family = AF_UNSPEC;
	hints.ai_socktype = stream ? SOCK_STREAM : SOCK_DGRAM;
	hints.ai_protocol = stream ? IPPROTO_TCP : IPPROTO_UDP;
	hints.ai_flags = passive ? AI_PASSIVE|AI_NUMERICHOST : AI_ADDRCONFIG;

	if (nai && *nai >= naimax)
		return -1;

	char buf[256], *host, *port;
	parse_host_port(hostport, passive, &host, &port, buf, sizeof(buf));
	if (host == NULL) {
		if (passive)
			host = "0.0.0.0";
		else
			perrordie("%s: host part missing", hostport);
	}
	if (port == NULL)
		port = dflport;

	int r = getaddrinfo(host, port, &hints, &res);
	if (r != 0)
		perrordie("%s: %s", hostport, gai_strerror(r));

	if (nai) {
		ai[*nai] = res;
		(*nai)++;
	} else {
		*ai = res;
	}

	return 0;
}

int sock_listen(char *hostport, int stream, int *sock, int *nsock, int nsmax)
{
	struct addrinfo	*res, *ai;
	resolve(hostport, "119", stream, 1, &res, NULL, 0);

	for (ai = res; ai; ai = ai->ai_next) {
		if (*nsock >= nsmax)
			break;
		int s = socket(ai->ai_family, ai->ai_socktype, ai->ai_protocol);
		if (s < 0)
			perrordie("socket: %m");
		int on = 1;
		if (ai->ai_family == AF_INET6) {
			setsockopt(s, IPPROTO_IPV6,
				   IPV6_V6ONLY, &on, sizeof(on));
		}
		setsockopt(s, SOL_SOCKET, SO_REUSEADDR, &on, sizeof(on));
		if (bind(s, ai->ai_addr, ai->ai_addrlen) < 0)
			perrordie("socket: bind(%s): %m", hostport);
		sock[(*nsock)++] = s;
	}
	return 0;
}

void tcp_listen(char *opt, char **name, int *sock, int *nsock, int max)
{
	char buf[256];
	strncpy(buf, opt, sizeof(buf));
	buf[sizeof(buf)-1] = 0;

	char *pp;
	char *h = strtok_r(buf, ",", &pp);
	do {
		int i, n = *nsock;
		sock_listen(h, 1, sock, nsock, max);
		for (i = n; i < *nsock; i++)
			name[i] = strdup(h);
	} while ((h = strtok_r(NULL, ",", &pp)) != NULL);
}

void do_exec(int sock, char *cmd, char *remote)
{
	char host[128];
	snprintf(host, sizeof(host), "%s", "unknown");

	struct sockaddr_storage ss;
	struct sockaddr *sa = (struct sockaddr *)&ss;
	socklen_t slen = sizeof(ss);
	if (getpeername(sock, sa, &slen) == 0) {
		getnameinfo(sa, slen, host, sizeof(host), NULL, 0, 0);
	}

	char *arg0;
	if ((arg0 = strrchr(cmd, '/')) != NULL) {
		arg0++;
	} else {
		arg0 = cmd;
	}

	setenv("REALSERVERS", remote, 1);

	dup2(sock, 0);
	dup2(sock, 1);
	dup2(sock, 2);
	if (sock > 2) close(sock);
	execl(cmd, arg0, host, NULL);

	perrordie("execl(%s): %m", cmd);
}

void usage(void)
{
	fprintf(stderr, "Usage: %s -l listenaddr,[addr,...] "
			"-r remoteaddr,[remoteaddr,...] [options]\n", progname);
	fprintf(stderr, "  listenaddr:        host:port or host or port\n");
	fprintf(stderr, "  remoteaddr:        host or host:port\n");
	fprintf(stderr, "  options:\n");
	fprintf(stderr, "    -f:              foreground\n");
	fprintf(stderr, "    -p file:         pidfile\n");
	fprintf(stderr, "    -s file:         server to run (xs-nntp-slb-go)\n");
	exit(1);
}

int main(int argc, char **argv)
{
	struct pollfd		pfd[MAXLISTEN + 1];
	pid_t			pid;
	char			*pidfile = NULL;
	char			*lname[MAXLISTEN];
	int			lsock[MAXLISTEN];
	int			numlisten = 0;
	int			i, n;
	int			c;
	int			devnull;
	int			do_foreground = 0;
	char			*remote = NULL;
	char			*slb;
	char			*s;
	char			tmp[128];
	char			slb_[128];

	snprintf(tmp, sizeof(tmp), "%s", argv[0]);
	if ((s = strrchr(tmp, '/')) != NULL) {
		*s = 0;
		snprintf(slb_, sizeof(slb_), "%s/%s", tmp, "xs-nntp-slb-go");
		slb = slb_;
	}

	if ((progname = strrchr(argv[0], '/')) != NULL)
		progname++;
	else
		progname = argv[0];
	openlog(progname, LOG_PID, LOG_NEWS);

	for (i = 0; i < MAXLISTEN; i++)
		lsock[i] = -1;

	while ((c = getopt(argc, argv, "Ndfl:np:r:s:")) != -1) switch(c) {
		case 'N':
		case 'n':
			break;
		case 'd':
			do_debug = 1;
			do_syslog = 0;
			do_foreground = 1;
			break;
		case 'f':
			do_foreground = 1;
			break;
		case 'l':
			tcp_listen(optarg, lname, lsock, &numlisten,
					MAXLISTEN - numlisten);
			break;
		case 'p':
			pidfile = optarg;
			break;
		case 'r':
			remote = optarg;
			break;
		case 's':
			slb = optarg;
			break;
		default:
			usage();
			break;
	}

	do {
		devnull = open("/dev/null", O_RDWR);
	} while (devnull >= 0 && devnull < 3);
	if (devnull < 0)
		perrordie("/dev/null: %m");

	if (argc != optind || numlisten == 0 || remote == NULL) {
		usage();
	}

	if (!do_foreground) {
		do_stderr = 0;
		pid = fork();
		if (pid < 0)
			perrordie("fork: %m");
		if (pid > 0)
			exit(0);
		dup2(devnull, 0);
		dup2(devnull, 1);
		dup2(devnull, 2);
		setsid();
	}
	close(devnull);
	if (pidfile) {
		FILE *fp = fopen(pidfile, "w");
		if (fp) {
			fprintf(fp, "%lu\n", (unsigned long)getpid());
			fclose(fp);
		}
	}

	for (i = 0; i < numlisten; i++) {
		if (listen(lsock[i], 16) < 0)
			perrordie("listen(%s): %m", lname[i]);
		if (blocking(lsock[i], 0) < 0)
			perrordie("fcntl(O_NONBLOCK): %m", lname[i]);
		pfd[i].fd = lsock[i];
		pfd[i].events = POLLIN;
	}

	signal(SIGPIPE, SIG_IGN);
	signal(SIGCHLD, SIG_IGN);

	while (1) {
		int r = poll(pfd, numlisten, -1);
		if (r < 0) {
			if (errno == EINTR)
				continue;
			perrordie("poll: %m");
		}
		for (i = 0; i < numlisten; i++) {
			if (!(pfd[i].revents & POLLIN))
				continue;
			n = accept(pfd[i].fd, NULL, NULL);
			if (n < 0) {
				if (errno == EAGAIN)
					continue;
				if (errno == EINTR)
					continue;
				perrordie("accept: %m");
			}
			blocking(n, 1);
			pid = fork();
			if (pid == 0) {
				for (i = 0; i < numlisten; i++)
					close(lsock[i]);
				signal(SIGPIPE, SIG_DFL);
				signal(SIGCHLD, SIG_DFL);
				do_exec(n, slb, remote);
				exit(1);
			}
			if (pid < 0)
				notice("fork: %m");
			close(n);
		}
	}

	return 0;
}

