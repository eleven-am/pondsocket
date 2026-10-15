package pondsocket

import (
	"context"
	"errors"
)

type membershipCause string

func (c membershipCause) Error() string {
	return string(c)
}

const connectionClosed membershipCause = "connection_closed"

type channelMembership struct {
	connCtx        context.Context
	releaseConnCtx context.CancelFunc
	ctx            context.Context
	cancel         context.CancelCauseFunc
	stopConnection func() bool
	stopCleanup    func() bool
}

func newChannelMembership(channelCtx context.Context, conn Transport) *channelMembership {
	ctx, cancel := context.WithCancelCause(channelCtx)
	connCtx, releaseConnCtx := membershipConnectionContext(conn)
	return &channelMembership{
		connCtx:        connCtx,
		releaseConnCtx: releaseConnCtx,
		ctx:            ctx,
		cancel:         cancel,
	}
}

func (m *channelMembership) watch(cleanup func()) {
	m.stopConnection = context.AfterFunc(m.connCtx, func() {
		m.cancel(connectionClosed)
	})
	m.stopCleanup = context.AfterFunc(m.ctx, cleanup)
}

func (m *channelMembership) close(reason string) {
	if m.stopConnection != nil {
		m.stopConnection()
	}
	if m.stopCleanup != nil {
		m.stopCleanup()
	}
	m.cancel(membershipCause(reason))
	m.releaseConnCtx()
}

func (m *channelMembership) reason() string {
	cause := context.Cause(m.ctx)
	if cause == nil {
		return string(connectionClosed)
	}
	if errors.Is(cause, context.Canceled) {
		return "channel_closed"
	}
	return cause.Error()
}

func (m *channelMembership) active() bool {
	select {
	case <-m.ctx.Done():
		return false
	case <-m.connCtx.Done():
		return false
	default:
		return true
	}
}

func membershipConnectionContext(conn Transport) (context.Context, context.CancelFunc) {
	if provider, ok := conn.(interface{ Context() context.Context }); ok {
		return provider.Context(), func() {}
	}

	ctx, cancel := context.WithCancel(context.Background())
	conn.OnClose(func(Transport) error {
		cancel()
		return nil
	})
	if !conn.IsActive() {
		cancel()
	}
	return ctx, cancel
}
