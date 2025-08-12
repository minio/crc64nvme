// Copyright (c) 2025 Minio Inc. All rights reserved.
// Use of this source code is governed by a license that can be
// found in the LICENSE file.

package crc64nvme

import (
	"bytes"
	"fmt"
	"hash"
	"hash/crc64"
	"io"
	"math/rand"
	"runtime"
	"sync/atomic"
	"testing"
)

var crc64Table = crc64.MakeTable(NVME)

func TestChecksum(t *testing.T) {
	if hasAsm {
		if hasAsm512 {
			testChecksum(t, "asm512-")
			hasAsm512 = false
			defer func() {
				hasAsm512 = true
			}()
		}
		testChecksum(t, "asm-")
		hasAsm = false
		testChecksum(t, "")
		hasAsm = true
	} else {
		testChecksum(t, "")
	}
}

func testChecksum(t *testing.T, asm string) {
	sizes := []int{0, 1, 3, 7, 8, 9, 15, 17, 127, 128, 129, 255, 256, 257, 511, 512, 513, 1e3, 1e4, 1e5, 1e6}
	for _, size := range sizes {
		t.Run(fmt.Sprintf("%ssize=%d", asm, size), func(t *testing.T) {
			rng := rand.New(rand.NewSource(int64(size)))
			for align := range 16 {
				t.Run(fmt.Sprintf("align=%d", align), func(t *testing.T) {
					data := make([]byte, size+align)[align:]
					rng.Read(data)
					ref := crc64.Checksum(data, crc64Table)
					got := Checksum(data)
					if got != ref {
						t.Errorf("got 0x%x, want 0x%x", got, ref)
					}
				})
			}
		})
	}
}

func TestHasher(t *testing.T) {
	if hasAsm {
		if hasAsm512 {
			testChecksum(t, "asm512-")
			hasAsm512 = false
			defer func() {
				hasAsm512 = true
			}()
		}
		testHasher(t, "asm-")
		hasAsm = false
		testHasher(t, "")
		hasAsm = true
	} else {
		testHasher(t, "")
	}
}

func testHasher(t *testing.T, asm string) {
	sizes := []int{0, 1, 3, 7, 8, 9, 15, 17, 127, 128, 129, 255, 256, 257, 383, 384, 385, 1e3, 1e4, 1e5, 1e6}
	for _, size := range sizes {
		t.Run(fmt.Sprintf("%ssize=%d", asm, size), func(t *testing.T) {
			rng := rand.New(rand.NewSource(int64(size)))
			data := make([]byte, size)
			rng.Read(data)
			ref := crc64.Checksum(data, crc64Table)
			h := New()
			io.CopyBuffer(h, bytes.NewReader(data), make([]byte, 17))
			got := h.Sum64()
			if got != ref {
				t.Errorf("got 0x%x, want 0x%x", got, ref)
			}
		})
	}
}

func TestLoopAlignment(t *testing.T) {
	for l := 128; l <= 128*10; l++ {
		dataBlock := make([]byte, l)
		for i := range dataBlock {
			dataBlock[i] = byte(i + 1)
		}

		// make sure we don't start on an aligned boundary
		offset := rand.Intn(16)
		data := dataBlock[offset:]

		ref := crc64.Checksum(data, crc64Table)
		got := update(0, data)
		if got != ref {
			t.Errorf("got 0x%x, want 0x%x", got, ref)
		}
	}
}

func setAsm(asm, asm512 bool) (reset func()) {
	oldAsm := hasAsm
	oldAsm512 := hasAsm512
	hasAsm = asm
	hasAsm512 = asm512
	return func() {
		hasAsm = oldAsm
		hasAsm512 = oldAsm512
	}
}

func BenchmarkCrc64(b *testing.B) {
	for _, sz := range []int64{64 << 20, 4 << 20, 1 << 20, 64 << 10, 4 << 10, 1 << 10, 128, 10} {
		if hasAsm512 {
			b.Run(fmt.Sprint(sz, "-asm512"), func(b *testing.B) {
				defer setAsm(true, true)()
				bench(b, New(), sz)
			})
		}
		if hasAsm {
			b.Run(fmt.Sprint(sz, "-asm"), func(b *testing.B) {
				defer setAsm(true, false)()
				bench(b, New(), sz)
			})
		}
		b.Run(fmt.Sprint(sz, "-go"), func(b *testing.B) {
			defer setAsm(false, false)()
			bench(b, New(), sz)
		})
		b.Run(fmt.Sprint(sz, "-stdlib"), func(b *testing.B) {
			bench(b, crc64.New(crc64Table), sz)
		})
	}

}

func bench(b *testing.B, h hash.Hash64, size int64) {
	b.SetBytes(size)
	data := make([]byte, size)
	for i := range data {
		data[i] = byte(i)
	}
	in := make([]byte, 0, h.Size())

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		h.Reset()
		h.Write(data)
		h.Sum(in)
	}
}

func benchmarkParallel(b *testing.B, size int) {
	hashes := make([]hash.Hash64, runtime.GOMAXPROCS(0))
	for i := range hashes {
		hashes[i] = New()
	}

	rng := rand.New(rand.NewSource(0xabadc0cac01a))
	data := make([][]byte, runtime.GOMAXPROCS(0))
	for i := range data {
		data[i] = make([]byte, size)
		rng.Read(data[i])
	}

	b.SetBytes(int64(size))
	b.ResetTimer()

	counter := uint64(0)
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			index := atomic.AddUint64(&counter, 1)
			index = index % uint64(len(data))
			hashes[index].Reset()
			hashes[index].Write(data[index])
			in := make([]byte, 0, hashes[index].Size())
			hashes[index].Sum(in)
		}
	})
}

func BenchmarkParallel(b *testing.B) {
	// go test -cpu=1,2,3,4,5,6,7,8,9,10,11,12,13,14,15,16 -bench=Parallel
	b.Run("1KB", func(b *testing.B) {
		benchmarkParallel(b, 1*1024)
	})
	b.Run("10KB", func(b *testing.B) {
		benchmarkParallel(b, 10*1024)
	})
	b.Run("1M", func(b *testing.B) {
		benchmarkParallel(b, 1*1024*1024)
	})
	b.Run("50M", func(b *testing.B) {
		benchmarkParallel(b, 50*1024*1024)
	})
}
