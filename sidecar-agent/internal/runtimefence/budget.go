package runtimefence

import (
	"math"

	"github.com/xtls/xray-core/common/buf"
	"github.com/xtls/xray-core/features/stats"
)

// admitBytes reserves one counted prefix across every UP/DOWN stream owned by
// this email in this physical boot. The same lock protects control generations,
// final counter reads and cumulative credit; reconnects never reset credit.
// Counters retain the pinned Xray semantics: count before handing bytes to the
// next layer, with no refund when a writer cannot report a partial byte count.
func (s *stream) admitBytes(mb buf.MultiBuffer, counter stats.Counter, datagram bool) (buf.MultiBuffer, bool) {
	g := s.gate
	g.mu.Lock()
	defer g.mu.Unlock()
	u := s.owner
	now, err := g.nowLocked()
	if err == nil {
		g.expireLocked(u, now)
	}
	if err != nil || s.closing || !u.allowed || g.closed || u.budgetExhausted {
		buf.ReleaseMulti(mb)
		return nil, true
	}
	remaining := int64(math.MaxInt64) - u.cumulativeBytes
	if u.byteLimited {
		remaining = u.byteCeiling - u.cumulativeBytes
	}
	admitted := make(buf.MultiBuffer, 0, len(mb))
	var count int64
	terminal := false
	for i, b := range mb {
		if b == nil {
			continue
		}
		n := int64(b.Len())
		if n <= remaining {
			admitted = append(admitted, b)
			mb[i] = nil
			count += n
			remaining -= n
			continue
		}
		// A datagram is indivisible, including an XUDP buffer carrying its
		// own destination. Only a byte-stream buffer may yield a prefix.
		if remaining > 0 && !datagram && b.UDP == nil {
			b.Resize(0, int32(remaining))
			admitted = append(admitted, b)
			mb[i] = nil
			count += remaining
			remaining = 0
		}
		buf.ReleaseMulti(mb[i:])
		terminal = true
		break
	}
	u.cumulativeBytes += count
	if count != 0 {
		counter.Add(count)
	}
	if remaining == 0 {
		u.budgetExhausted = true
		terminal = true
	}
	// Exhaustion closes admission immediately, without interrupting the last
	// prefix before its underlying write or the already-counted pipe readers.
	// Natural EOF/error, the lease deadline and explicit fencing retain their
	// existing worker/I/O/stop drain guarantees.
	return admitted, terminal
}
