package echonet

import (
	"context"
	"fmt"
)

// PropertyMaps reports advertised access capabilities. Unknown maps are nil, not empty.
type PropertyMaps struct {
	Get          []EPC
	Set          []EPC
	Announcement []EPC
}

func (o *Object) PropertyMaps(ctx context.Context) (PropertyMaps, error) {
	r, requestErr := o.Get(ctx, GetPropertyMap, SetPropertyMap, AnnouncementPropertyMap)
	var maps PropertyMaps
	if r == nil {
		return maps, requestErr
	}
	for _, entry := range []struct {
		epc EPC
		dst *[]EPC
	}{{GetPropertyMap, &maps.Get}, {SetPropertyMap, &maps.Set}, {AnnouncementPropertyMap, &maps.Announcement}} {
		b, ok := r.Property(entry.epc)
		if !ok || len(b) == 0 {
			continue
		}
		v, err := parsePropertyMap(b)
		if err != nil {
			return maps, fmt.Errorf("EPC %02x: %w", entry.epc, err)
		}
		*entry.dst = v
	}
	return maps, requestErr
}

func parsePropertyMap(b []byte) ([]EPC, error) {
	if len(b) == 0 {
		return nil, fmt.Errorf("missing property map")
	}
	n := int(b[0])
	out := make([]EPC, 0, n)
	if n < 16 {
		if len(b) != n+1 {
			return nil, fmt.Errorf("invalid list map length")
		}
		seen := map[EPC]bool{}
		for _, v := range b[1:] {
			e := EPC(v)
			if v < 0x80 || seen[e] {
				return nil, fmt.Errorf("invalid property map entry")
			}
			seen[e] = true
			out = append(out, e)
		}
	} else {
		if len(b) != 17 {
			return nil, fmt.Errorf("invalid bitmap length")
		}
		for low, v := range b[1:] {
			for high := 0; high < 8; high++ {
				if v&(1<<high) != 0 {
					out = append(out, EPC((high+8)*16+low))
				}
			}
		}
		if len(out) != n {
			return nil, fmt.Errorf("bitmap count mismatch")
		}
	}
	return out, nil
}
