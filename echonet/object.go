package echonet

import (
	"context"
	"fmt"

	"github.com/otknoy/echonet-lite/internal/netutil"
)

func validProperties(ps []Property, write bool) error {
	if len(ps) == 0 || len(ps) > 255 {
		return fmt.Errorf("%w: property count must be 1..255", ErrInvalid)
	}
	seen := map[EPC]bool{}
	for _, p := range ps {
		if seen[p.EPC] || p.EPC < 0x80 {
			return fmt.Errorf("%w: duplicate or invalid EPC", ErrInvalid)
		}
		seen[p.EPC] = true
		if len(p.EDT) > 255 || (write && len(p.EDT) == 0) {
			return fmt.Errorf("%w: EDT length", ErrInvalid)
		}
	}
	return nil
}
func (o *Object) Get(ctx context.Context, epcs ...EPC) (*Response, error) {
	ps := make([]Property, len(epcs))
	for i, e := range epcs {
		ps[i].EPC = e
	}
	return o.request(ctx, Get, ps)
}
func (o *Object) SetC(ctx context.Context, ps ...Property) (*Response, error) {
	return o.request(ctx, SetC, ps)
}
func (o *Object) request(ctx context.Context, esv ESV, ps []Property) (*Response, error) {
	if !netutil.IsUnicastIPv4(o.node.address) || o.eoj[2] == 0 {
		return nil, fmt.Errorf("%w: unicast IP and concrete EOJ required", ErrInvalid)
	}
	if err := validProperties(ps, esv == SetC); err != nil {
		return nil, err
	}
	c := o.node.client
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	p := &pending{address: o.node.address, request: frame{seoj: controllerEOJ, deoj: o.eoj, esv: esv, properties: ps}, responses: make(chan *Response, 1)}
	release, err := c.sendPending(ctx, p)
	if err != nil {
		return nil, err
	}
	defer release()
	unknown := func(err error) (*Response, error) {
		if esv == SetC {
			return nil, &SettingUnknownError{err}
		}
		return nil, err
	}
	select {
	case r := <-p.responses:
		if r.ESV == GetSNA || r.ESV == SetCSNA {
			return r, &ServiceError{r}
		}
		return r, nil
	case <-ctx.Done():
		return unknown(ctx.Err())
	case <-c.closed:
		return unknown(c.failure())
	}
}
