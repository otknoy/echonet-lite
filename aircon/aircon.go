// Package aircon provides typed operations for household air conditioner objects.
package aircon

import (
	"context"
	"errors"
	"fmt"

	"github.com/otknoy/echonet-lite/echonet"
)

const (
	OperationStatus    echonet.EPC = 0x80
	OperationMode      echonet.EPC = 0xb0
	TemperatureSetting echonet.EPC = 0xb3
	RoomTemperature    echonet.EPC = 0xbb
)

type Mode byte

const (
	Other Mode = 0x40
	Auto  Mode = 0x41
	Cool  Mode = 0x42
	Heat  Mode = 0x43
	Dry   Mode = 0x44
	Fan   Mode = 0x45
)

func (m Mode) String() string {
	switch m {
	case Other:
		return "other"
	case Auto:
		return "auto"
	case Cool:
		return "cool"
	case Heat:
		return "heat"
	case Dry:
		return "dry"
	case Fan:
		return "fan"
	}
	return fmt.Sprintf("unknown(%02x)", byte(m))
}
func (m Mode) MarshalText() ([]byte, error) { return []byte(m.String()), nil }
func ParseMode(s string) (Mode, error) {
	for _, m := range []Mode{Auto, Cool, Heat, Dry, Fan} {
		if m.String() == s {
			return m, nil
		}
	}
	return 0, fmt.Errorf("%w: mode must be auto, cool, heat, dry or fan", echonet.ErrInvalid)
}

type objectAPI interface {
	Get(context.Context, ...echonet.EPC) (*echonet.Response, error)
	SetC(context.Context, ...echonet.Property) (*echonet.Response, error)
	PropertyMaps(context.Context) (echonet.PropertyMaps, error)
}
type Aircon struct{ object objectAPI }

func IsAircon(e echonet.EOJ) bool { return e[0] == 1 && e[1] == 0x30 && e[2] != 0 }
func New(object *echonet.Object) (*Aircon, error) {
	if object == nil || !IsAircon(object.EOJ()) {
		return nil, fmt.Errorf("%w: household air conditioner EOJ required", echonet.ErrInvalid)
	}
	return &Aircon{object}, nil
}

// Field explicitly distinguishes unsupported or unavailable values from numeric zero.
type Field[T any] struct {
	Value *T     `json:"value"`
	State string `json:"state"`
	Error string `json:"error,omitempty"`
}
type Status struct {
	Power           Field[bool] `json:"power"`
	Mode            Field[Mode] `json:"mode"`
	Temperature     Field[int]  `json:"temperature"`
	RoomTemperature Field[int]  `json:"room_temperature"`
}

func contains(es []echonet.EPC, e echonet.EPC) bool {
	for _, v := range es {
		if v == e {
			return true
		}
	}
	return false
}
func decode[T any](r *echonet.Response, epc echonet.EPC, supported bool, requestErr error, convert func(byte) (T, error)) (Field[T], error) {
	if !supported {
		return Field[T]{State: "unsupported"}, nil
	}
	data, ok := r.Property(epc)
	fail := func(err error) (Field[T], error) {
		return Field[T]{State: "error", Error: err.Error()}, fmt.Errorf("EPC %02x: %w", epc, err)
	}
	if !ok || len(data) == 0 {
		if requestErr == nil {
			requestErr = fmt.Errorf("missing value")
		}
		return fail(requestErr)
	}
	if len(data) != 1 {
		return fail(fmt.Errorf("invalid EDT length %d", len(data)))
	}
	value, err := convert(data[0])
	if err != nil {
		return fail(err)
	}
	return Field[T]{Value: &value, State: "ok"}, nil
}
func failedField[T any](err error) Field[T] { return Field[T]{State: "error", Error: err.Error()} }
func failedStatus(err error) Status {
	return Status{Power: failedField[bool](err), Mode: failedField[Mode](err), Temperature: failedField[int](err), RoomTemperature: failedField[int](err)}
}
func (a *Aircon) Status(ctx context.Context) (Status, error) {
	var status Status
	maps, mapErr := a.object.PropertyMaps(ctx)
	var rejected *echonet.ServiceError
	if mapErr != nil && (!errors.As(mapErr, &rejected) || maps.Get == nil) {
		return failedStatus(mapErr), mapErr
	}
	if maps.Get == nil {
		err := fmt.Errorf("get property map unavailable")
		return failedStatus(err), err
	}
	epcs := []echonet.EPC{}
	for _, e := range []echonet.EPC{OperationStatus, OperationMode, TemperatureSetting, RoomTemperature} {
		if contains(maps.Get, e) {
			epcs = append(epcs, e)
		}
	}
	var r *echonet.Response
	var requestErr error
	if len(epcs) > 0 {
		r, requestErr = a.object.Get(ctx, epcs...)
	}
	var errs []error
	var err error
	status.Power, err = decode(r, OperationStatus, contains(maps.Get, OperationStatus), requestErr, DecodePower)
	errs = append(errs, err)
	status.Mode, err = decode(r, OperationMode, contains(maps.Get, OperationMode), requestErr, DecodeMode)
	errs = append(errs, err)
	status.Temperature, err = decode(r, TemperatureSetting, contains(maps.Get, TemperatureSetting), requestErr, DecodeTemperature)
	errs = append(errs, err)
	status.RoomTemperature, err = decode(r, RoomTemperature, contains(maps.Get, RoomTemperature), requestErr, DecodeRoomTemperature)
	errs = append(errs, err)
	return status, errors.Join(errs...)
}
func DecodePower(b byte) (bool, error) {
	if b == 0x30 {
		return true, nil
	}
	if b == 0x31 {
		return false, nil
	}
	return false, fmt.Errorf("invalid power value %02x", b)
}
func DecodeMode(b byte) (Mode, error) {
	if b >= 0x40 && b <= 0x45 {
		return Mode(b), nil
	}
	return 0, fmt.Errorf("invalid mode value %02x", b)
}
func DecodeTemperature(b byte) (int, error) {
	if b <= 50 {
		return int(b), nil
	}
	return 0, fmt.Errorf("temperature unavailable or invalid (%02x)", b)
}
func DecodeRoomTemperature(b byte) (int, error) {
	if b == 0x80 || b == 0x7e || b == 0x7f {
		return 0, fmt.Errorf("room temperature unavailable or out of range (%02x)", b)
	}
	return int(int8(b)), nil
}
func (a *Aircon) set(ctx context.Context, epc echonet.EPC, value byte) error {
	maps, err := a.object.PropertyMaps(ctx)
	var rejected *echonet.ServiceError
	if err != nil && (!errors.As(err, &rejected) || maps.Set == nil) {
		return err
	}
	if maps.Set == nil {
		return fmt.Errorf("set property map unavailable")
	}
	if !contains(maps.Set, epc) {
		return fmt.Errorf("%w: EPC %02x", echonet.ErrUnsupported, epc)
	}
	_, err = a.object.SetC(ctx, echonet.Property{EPC: epc, EDT: []byte{value}})
	return err
}
func (a *Aircon) SetPower(ctx context.Context, on bool) error {
	value := byte(0x31)
	if on {
		value = 0x30
	}
	return a.set(ctx, OperationStatus, value)
}
func (a *Aircon) SetMode(ctx context.Context, mode Mode) error {
	if mode < Auto || mode > Fan {
		return fmt.Errorf("%w: invalid mode", echonet.ErrInvalid)
	}
	return a.set(ctx, OperationMode, byte(mode))
}

// SetTemperature accepts the standard 0..50 C domain. Narrower device/mode limits
// are enforced by the remote device and returned as a ServiceError.
func (a *Aircon) SetTemperature(ctx context.Context, celsius int) error {
	if celsius < 0 || celsius > 50 {
		return fmt.Errorf("%w: temperature must be 0..50 C", echonet.ErrInvalid)
	}
	return a.set(ctx, TemperatureSetting, byte(celsius))
}
