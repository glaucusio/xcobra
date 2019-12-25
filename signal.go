package xcobra

import (
	"context"
	"os"
	"os/signal"
	"sync/atomic"
)

var gsigh = newSighandler(context.Background())

func WithSignal(ctx context.Context, signals ...os.Signal) (context.Context, context.CancelFunc) {
	return gsigh.withSignal(ctx, signals...)
}

type sighandler struct {
	args   chan sig
	queues map[os.Signal][]*cancelFunc
	ch     chan os.Signal
}

type sig struct {
	fn   context.CancelFunc
	sigs []os.Signal
	stop chan<- context.CancelFunc
}

func newSighandler(ctx context.Context) *sighandler {
	sigh := &sighandler{
		args:   make(chan sig),
		queues: make(map[os.Signal][]*cancelFunc),
		ch:     make(chan os.Signal),
	}
	go sigh.join(ctx)
	return sigh
}

func (sigh *sighandler) withSignal(ctx context.Context, signals ...os.Signal) (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithCancel(ctx)
	stop := make(chan context.CancelFunc, 1)

	sigh.args <- sig{
		fn:   cancel,
		sigs: signals,
		stop: stop,
	}

	return ctx, <-stop
}

func (sigh *sighandler) stop() {
	signal.Stop(sigh.ch)
	for _, queue := range sigh.queues {
		for _, cancel := range queue {
			cancel.do()
		}
	}
	sigh.queues = nil
}

func (sigh *sighandler) join(ctx context.Context) {
	defer sigh.stop()

	for {
		select {
		case <-ctx.Done():
			return

		case s := <-sigh.ch:
			fn, queue := (*cancelFunc)(nil), sigh.queues[s]

			for len(queue) != 0 {
				if fn, queue = queue[0], queue[1:]; fn.do() {
					break
				}
			}

			sigh.queues[s] = queue

		case arg, ok := <-sigh.args:
			if !ok {
				return
			}

			fn := &cancelFunc{
				fn: arg.fn,
			}

			n := len(sigh.queues)

			for _, s := range arg.sigs {
				sigh.queues[s] = append(sigh.queues[s], fn)
			}

			if len(sigh.queues) > n {
				signal.Stop(sigh.ch)

				var sigs []os.Signal
				for s := range sigh.queues {
					sigs = append(sigs, s)
				}

				signal.Notify(sigh.ch, sigs...)
			}

			arg.stop <- func() { fn.do() }
		}
	}
}

type cancelFunc struct {
	done int32
	fn   context.CancelFunc
}

func (cfn *cancelFunc) do() bool {
	if !atomic.CompareAndSwapInt32(&cfn.done, 0, 1) {
		return false
	}
	cfn.fn()
	return true
}
