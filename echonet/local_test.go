package echonet

import (
	"bytes"
	"context"
	"encoding/hex"
	"net/netip"
	"reflect"
	"testing"
	"time"
)

type localSendRecorder struct {
	*fakeTransport
	attempts int
}

func (r *localSendRecorder) WritePacket(ctx context.Context, b []byte, to netip.Addr) error {
	r.attempts++
	return r.fakeTransport.WritePacket(ctx, b, to)
}

// These wire vectors independently specify headers and service codes. No
// transport or worker goroutines are needed to build a local response.
func TestLocalResponseVectors(t *testing.T) {
	tests := []struct{ name, request, response string }{
		{"Get", "1081002a01300105ff0162018000", "1081002a05ff010130017201800130"},
		{"Get partial SNA", "1081002a01300105ff0162028000fe00", "1081002a05ff010130015202800130fe00"},
		{"Get unavailable", "1081002a01300105ff016201fe00", "1081002a05ff010130015201fe00"},
		{"SetC rejected", "1081002a01300105ff016101800131", "1081002a05ff010130015101800131"},
		{"SetI rejected", "1081002a01300105ff016001800131", "1081002a05ff010130015001800131"},
		{"INF_REQ", "1081002a01300105ff0163018000", "1081002a05ff010130017301800130"},
		{"INF_REQ partial SNA", "1081002a01300105ff0163028000fe00", "1081002a05ff010130015302800130fe00"},
		{"node instance notification", "1081002a0130010ef0016301d500", "1081002a0ef0010130017301d5040105ff01"},
		{"controller has no instance notification", "1081002a01300105ff016301d500", "1081002a05ff010130015301d500"},
		{"Get has no instance notification", "1081002a0130010ef0016201d500", "1081002a0ef0010130015201d500"},
		{"INFC", "1081002a01300105ff017402800130fe01ff", "1081002a05ff010130017a028000fe00"},
		{"INFC empty EDT", "1081002a01300105ff0174018000", "1081002a05ff010130017a018000"},
		{"SetGet unsupported", "1081002a01300105ff016e0180013001bb00", "1081002a05ff010130015e0000"},
		{"SetGet rejected without executing", "1081002a01300105ff016e01800001bb0130", "1081002a05ff010130015e0000"},
		{"Get nonempty EDT", "1081002a01300105ff016201800130", ""},
		{"INF_REQ nonempty EDT", "1081002a01300105ff016301800130", ""},
		{"SetC empty EDT", "1081002a01300105ff0161018000", ""},
		{"SetI empty EDT", "1081002a01300105ff0160018000", ""},
		{"Get invalid second EDT", "1081002a01300105ff0162028000fe0130", ""},
		{"SetC invalid second EDT", "1081002a01300105ff016102800130fe00", ""},
		{"unsupported service", "1081002a01300105ff0164018000", ""},
		{"incoming response", "1081002a01300105ff017201800130", ""},
		{"unknown object", "1081002a01300105ff0262018000", ""},
	}
	c := &Client{}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			wire, err := hex.DecodeString(tt.request)
			if err != nil {
				t.Fatal(err)
			}
			request, err := parseFrame(wire)
			if err != nil {
				t.Fatal(err)
			}
			properties := c.localProperties(request.deoj)
			originalProperties := c.localProperties(request.deoj)
			response, ok := localResponse(request, request.deoj, properties)
			if ok != (tt.response != "") {
				t.Fatalf("respond=%v, want response %q", ok, tt.response)
			}
			if ok {
				got, err := response.marshal()
				if err != nil {
					t.Fatal(err)
				}
				if hex.EncodeToString(got) != tt.response {
					t.Fatalf("got %x, want %s", got, tt.response)
				}
			}
			after, err := request.marshal()
			if err != nil || !bytes.Equal(after, wire) {
				t.Fatalf("request changed: %x (%v)", after, err)
			}
			if !reflect.DeepEqual(properties, originalProperties) {
				t.Fatal("local properties changed")
			}
		})
	}
}

func TestReplyLocalRouting(t *testing.T) {
	tests := []struct {
		name         string
		destination  EOJ
		wantResponse bool
		source       EOJ
	}{
		{"node", nodeEOJ, true, nodeEOJ},
		{"controller", controllerEOJ, true, controllerEOJ},
		{"node class", EOJ{0x0e, 0xf0, 0}, true, nodeEOJ},
		{"controller class", EOJ{0x05, 0xff, 0}, true, controllerEOJ},
		{"unknown instance", EOJ{0x05, 0xff, 2}, false, EOJ{}},
		{"unknown class", EOJ{0x01, 0x30, 0}, false, EOJ{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			transport := &localSendRecorder{fakeTransport: newFake()}
			c := &Client{transport: transport, timeout: time.Second}
			address := netip.MustParseAddr("192.0.2.20")
			c.replyLocal(localRequest{
				frame:   frame{tid: 42, seoj: acEOJ, deoj: tt.destination, esv: Get, properties: []Property{{EPC: 0x80}}},
				address: address,
			})
			wantAttempts := 0
			if tt.wantResponse {
				wantAttempts = 1
			}
			if transport.attempts != wantAttempts {
				t.Fatalf("send attempts=%d, want %d", transport.attempts, wantAttempts)
			}
			if len(transport.outgoing) != 0 {
				if !tt.wantResponse {
					t.Fatal("replied to an unknown local object")
				}
				sent := <-transport.outgoing
				response, err := parseFrame(sent.data)
				if err != nil {
					t.Fatal(err)
				}
				if sent.from != address || response.tid != 42 || response.seoj != tt.source || response.deoj != acEOJ || response.esv != GetRes {
					t.Fatalf("wrong destination or response: %v %+v", sent.from, response)
				}
				if len(transport.outgoing) != 0 {
					t.Fatal("duplicate response")
				}
			} else if tt.wantResponse {
				t.Fatal("missing response")
			}
		})
	}
}

func TestReplyLocalDoesNotSendInvalidResponses(t *testing.T) {
	oversized := make([]Property, 100)
	for i := range oversized {
		oversized[i].EPC = 0x83
	}
	for name, props := range map[string][]Property{
		"invalid EDT":        {{EPC: 0x80, EDT: []byte{0x30}}},
		"oversized response": oversized,
	} {
		t.Run(name, func(t *testing.T) {
			transport := &localSendRecorder{fakeTransport: newFake()}
			c := &Client{transport: transport, timeout: time.Second}
			c.replyLocal(localRequest{
				frame:   frame{tid: 42, seoj: acEOJ, deoj: nodeEOJ, esv: Get, properties: props},
				address: peer,
			})
			if transport.attempts != 0 || len(transport.outgoing) != 0 {
				t.Fatal("invalid response was sent")
			}
		})
	}
}
