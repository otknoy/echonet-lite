package aircon

import (
	"context"
	"errors"
	"net/netip"
	"testing"

	"echonet-lite/echonet"
)

type virtualAC struct {
	values   map[echonet.EPC][]byte
	maps     echonet.PropertyMaps
	getErr   error
	setErr   error
	mapErr   error
	requests int
	writes   int
}

func virtual() *virtualAC {
	return &virtualAC{values: map[echonet.EPC][]byte{OperationStatus: {0x31}, OperationMode: {0x42}, TemperatureSetting: {24}, RoomTemperature: {0xfb}}, maps: echonet.PropertyMaps{Get: []echonet.EPC{OperationStatus, OperationMode, TemperatureSetting, RoomTemperature}, Set: []echonet.EPC{OperationStatus, OperationMode, TemperatureSetting}}}
}
func (v *virtualAC) PropertyMaps(context.Context) (echonet.PropertyMaps, error) {
	return v.maps, v.mapErr
}
func (v *virtualAC) Get(_ context.Context, epcs ...echonet.EPC) (*echonet.Response, error) {
	v.requests++
	r := &echonet.Response{ESV: echonet.GetRes}
	for _, e := range epcs {
		r.Properties = append(r.Properties, echonet.Property{EPC: e, EDT: v.values[e]})
	}
	return r, v.getErr
}
func (v *virtualAC) SetC(_ context.Context, ps ...echonet.Property) (*echonet.Response, error) {
	v.writes++
	if v.setErr != nil {
		return nil, v.setErr
	}
	for _, p := range ps {
		v.values[p.EPC] = append([]byte(nil), p.EDT...)
	}
	return &echonet.Response{ESV: echonet.SetRes}, nil
}
func TestTypedGetSetReadback(t *testing.T) {
	v := virtual()
	a := &Aircon{v}
	ctx := context.Background()
	if err := a.SetPower(ctx, true); err != nil {
		t.Fatal(err)
	}
	if err := a.SetMode(ctx, Heat); err != nil {
		t.Fatal(err)
	}
	if err := a.SetTemperature(ctx, 26); err != nil {
		t.Fatal(err)
	}
	s, err := a.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !*s.Power.Value || *s.Mode.Value != Heat || *s.Temperature.Value != 26 || *s.RoomTemperature.Value != -5 {
		t.Fatalf("%+v", s)
	}
	if v.requests != 1 || v.writes != 3 {
		t.Fatalf("gets %d writes %d", v.requests, v.writes)
	}
}
func TestPartialAndUnsupported(t *testing.T) {
	v := virtual()
	v.maps.Get = v.maps.Get[:3]
	a := &Aircon{v}
	s, err := a.Status(context.Background())
	if err != nil || s.RoomTemperature.State != "unsupported" || s.RoomTemperature.Value != nil {
		t.Fatalf("%+v %v", s, err)
	}
	v.maps.Get = append(v.maps.Get, RoomTemperature)
	delete(v.values, RoomTemperature)
	rejection := &echonet.ServiceError{Response: &echonet.Response{ESV: echonet.GetSNA}}
	v.getErr = rejection
	s, err = a.Status(context.Background())
	var sna *echonet.ServiceError
	if !errors.As(err, &sna) || s.RoomTemperature.State != "error" || s.Power.Value == nil {
		t.Fatalf("%+v %v", s, err)
	}
}
func TestValidationAndErrors(t *testing.T) {
	v := virtual()
	a := &Aircon{v}
	ctx := context.Background()
	for _, n := range []int{-1, 51, 256} {
		if err := a.SetTemperature(ctx, n); !errors.Is(err, echonet.ErrInvalid) {
			t.Fatal(err)
		}
	}
	if err := a.SetMode(ctx, Other); !errors.Is(err, echonet.ErrInvalid) {
		t.Fatal(err)
	}
	if v.writes != 0 {
		t.Fatal("invalid input sent")
	}
	v.maps.Set = []echonet.EPC{}
	if err := a.SetPower(ctx, true); !errors.Is(err, echonet.ErrUnsupported) {
		t.Fatal(err)
	}
	v.maps.Set = nil
	v.mapErr = context.DeadlineExceeded
	if err := a.SetPower(ctx, true); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal(err)
	}
	v = virtual()
	a = &Aircon{v}
	v.setErr = &echonet.SettingUnknownError{Cause: context.DeadlineExceeded}
	var unknown *echonet.SettingUnknownError
	if err := a.SetPower(ctx, true); !errors.As(err, &unknown) {
		t.Fatal(err)
	}
}
func TestDecodeSpecialValues(t *testing.T) {
	for _, b := range []byte{0x80, 0x7e, 0x7f} {
		if _, err := DecodeRoomTemperature(b); err == nil {
			t.Fatalf("accepted %x", b)
		}
	}
	for _, b := range []byte{0xfd, 0xff, 51} {
		if _, err := DecodeTemperature(b); err == nil {
			t.Fatalf("accepted %x", b)
		}
	}
	if n, err := DecodeRoomTemperature(0x81); err != nil || n != -127 {
		t.Fatalf("%d %v", n, err)
	}
	if n, err := DecodeTemperature(0); err != nil || n != 0 {
		t.Fatal("lost zero")
	}
	if _, err := DecodePower(0); err == nil {
		t.Fatal("invalid power")
	}
	if _, err := DecodeMode(0); err == nil {
		t.Fatal("invalid mode")
	}
	if _, err := ParseMode("invalid"); !errors.Is(err, echonet.ErrInvalid) {
		t.Fatal(err)
	}
}
func TestWrapEOJWithoutNetwork(t *testing.T) {
	// A zero-value client is sufficient for handle creation; no communication occurs.
	c := new(echonet.Client)
	o := c.Node(netip.MustParseAddr("192.0.2.1")).Object(echonet.EOJ{1, 0x30, 1})
	if _, err := New(o); err != nil {
		t.Fatal(err)
	}
	if _, err := New(c.Node(netip.MustParseAddr("192.0.2.1")).Object(echonet.EOJ{1, 0x30, 0})); err == nil {
		t.Fatal("accepted wildcard")
	}
	if _, err := New(nil); err == nil {
		t.Fatal("accepted nil")
	}
}

func TestMapFailureIsNotUnsupported(t *testing.T) {
	v := virtual()
	v.maps.Get = nil
	v.mapErr = context.DeadlineExceeded
	s, err := (&Aircon{v}).Status(context.Background())
	if !errors.Is(err, context.DeadlineExceeded) || s.Power.State != "error" || s.RoomTemperature.State != "error" {
		t.Fatalf("%+v %v", s, err)
	}
}
