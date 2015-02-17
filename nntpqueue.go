package main

import (
	"strings"
	"sync/atomic"
	"sync"
)

type NNTPReq struct {
	line	 string
	cmd	 string
	msgid    string
	code     int
	ready	 bool
}

type NNTPQueue struct {
	queue     []*NNTPReq
	qlock     sync.Mutex
	wlock     sync.Mutex
	sess      *NNTPSession
	lastcode  int32
}

//
//	For certain commands - right now only for STAT - we
//	add the message-id to the reply. This is for testing.
//
func (req *NNTPReq) addmsgid() {
	if req.code == 430 && len(req.msgid) > 0 &&
	   strings.IndexByte(req.line, '<') < 0 {
		var l int
		for l = len(req.line); l > 0; l-- {
			if req.line[l-1] != '\r' && req.line[l-1] != '\n' {
				break
			}
		}
		req.line = req.line[:l] + " " + req.msgid + "\r\n"
	}
}

//
//	Add a request to a queue. Possibly runs the queue.
//
func (q *NNTPQueue) Add(req *NNTPReq, run_queue bool) {
	q.qlock.Lock()
	defer q.qlock.Unlock()

	// Append this entry to the queue.
	q.queue = append(q.queue, req)
	if len(q.queue) == 1 && run_queue {
		q.run()
	}
}

//
//	Pop oldest entry off the queue and return it.
//
func (q *NNTPQueue) PopFirst() (r *NNTPReq) {
	q.qlock.Lock()
	defer q.qlock.Unlock()
	len := len(q.queue)
	if len > 0 {
		r = q.queue[0]
		q.queue = q.queue[1:len]
	}
	return
}

//
//	Examine the oldest entry. If it is ready, pop it
//	off the queue and 'run' it. Rinse and repeat.
//	Assume that q.qlock is already locked.
//
func (q *NNTPQueue) run() {

	if len(q.queue) == 0 || !q.queue[0].ready {
		return
	}
	q.wlock.Lock()

	Log.Debug("running queue, len is %d", len(q.queue))

	for {
		req := q.queue[0]
		q.queue = q.queue[1:]
		q.qlock.Unlock()

		// Do not copy replies to QUIT
		var err error
		if req.cmd != "quit" {
			req.addmsgid()
			err = q.sess.Write(req.line)
		}

		if err != nil {
			// whoops, remote client has gone away
			Log.Fatal("%s: lost connection(write) - force exit: " +
				"%s on %s", q.sess.name, err, req.line)
		}

		q.qlock.Lock()
		if len(q.queue) == 0 || !q.queue[0].ready {
			atomic.StoreInt32(&q.lastcode, int32(req.code))
			break;
		}
	}

	err := q.sess.Flush()
	Log.Debug("done running queue, len is %d\n", len(q.queue))
	q.wlock.Unlock()
	if err != nil {
		// whoops, remote client has gone away
		Log.Fatal("%s: lost connection(flush) - force exit: %s",
			q.sess.name, err)
	}
}

func (q *NNTPQueue) LastCode() int {
	return int(atomic.LoadInt32(&q.lastcode))
}

func (q *NNTPQueue) Run() {
	q.qlock.Lock()
	defer q.qlock.Unlock()
	q.run()
}

func (q *NNTPQueue) Len() int {
	q.qlock.Lock()
	defer q.qlock.Unlock()
	return len(q.queue)
}

//
//	Set entry to 'ready' and try to run the queue
//
func (q *NNTPQueue) Ready(r *NNTPReq) {
	q.qlock.Lock()
	defer q.qlock.Unlock()
	r.ready = true
	q.run()
}

