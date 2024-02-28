// Copyright 2023 LiveKit, Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package bucket

import (
	"encoding/binary"
	"fmt"
)

const (
	MaxPktSize     = 1500
	pktSizeHeader  = 2
	seqNumOffset   = 2
	seqNumSize     = 2
	invalidPktSize = uint16(65535)
)

type Bucket struct {
	slots              [][]byte
	init               bool
	resyncOnNextPacket bool
	step               int
	headSN             uint16
	initCapacity       int
	maxSteps           int
}

func NewBucket(capacity int) *Bucket {
	b := &Bucket{
		initCapacity: capacity,
		maxSteps:     capacity,
		slots:        make([][]byte, capacity),
	}

	pktSize := MaxPktSize + pktSizeHeader
	buf := make([]byte, pktSize*capacity)
	for i := 0; i < capacity; i++ {
		b.slots[i] = buf[i*pktSize : (i+1)*pktSize]
	}

	b.invalidate(0, b.maxSteps)
	return b
}

// Grow increases the capacity of the bucket by adding initial capacity to the buffer
func (b *Bucket) Grow() int {
	pktSize := MaxPktSize + pktSizeHeader
	buf := make([]byte, pktSize*b.initCapacity)
	newSlots := make([][]byte, b.initCapacity)
	for i := 0; i < b.initCapacity; i++ {
		newSlots[i] = buf[i*pktSize : (i+1)*pktSize]
		binary.BigEndian.PutUint16(newSlots[i], invalidPktSize)
	}

	growedSlots := append(b.slots, newSlots...)
	// move wrapped slots to new slots
	for i := b.maxSteps - 1; i >= b.step; i-- {
		if binary.BigEndian.Uint16(b.slots[i]) != invalidPktSize {
			growedSlots[i+b.initCapacity], b.slots[i] = b.slots[i], growedSlots[i+b.initCapacity]
		}
	}
	b.slots = growedSlots
	b.maxSteps += b.initCapacity
	return b.maxSteps
}

func (b *Bucket) ResyncOnNextPacket() {
	b.resyncOnNextPacket = true
}

func (b *Bucket) HeadSequenceNumber() uint16 {
	return b.headSN
}

func (b *Bucket) Capacity() int {
	return b.maxSteps
}

func (b *Bucket) addPacket(pkt []byte, sn uint16) ([]byte, error) {
	if len(pkt) > MaxPktSize-pktSizeHeader {
		return nil, ErrPacketTooLarge
	}

	if !b.init {
		b.headSN = sn - 1
		b.init = true
	}

	if b.resyncOnNextPacket {
		b.resyncOnNextPacket = false

		b.headSN = sn - 1
		b.invalidate(0, b.maxSteps)
	}

	diff := sn - b.headSN
	if diff == 0 || diff > (1<<15) {
		// duplicate of last packet or out-of-order
		return b.set(sn, pkt)
	}

	return b.push(sn, pkt)
}

func (b *Bucket) AddPacket(pkt []byte) ([]byte, error) {
	return b.addPacket(pkt, binary.BigEndian.Uint16(pkt[seqNumOffset:seqNumOffset+seqNumSize]))
}

func (b *Bucket) AddPacketWithSequenceNumber(pkt []byte, sn uint16) ([]byte, error) {
	storedPkt, err := b.addPacket(pkt, sn)
	if err != nil {
		return nil, err
	}

	// overwrite sequence number in packet
	binary.BigEndian.PutUint16(storedPkt[seqNumOffset:seqNumOffset+seqNumSize], sn)
	return storedPkt, nil
}

func (b *Bucket) GetPacket(buf []byte, sn uint16) (int, error) {
	p, err := b.get(sn)
	if err != nil {
		return 0, err
	}
	n := len(p)
	if cap(buf) < n {
		return 0, ErrBufferTooSmall
	}
	if len(buf) < n {
		buf = buf[:n]
	}
	copy(buf, p)
	return n, nil
}

func (b *Bucket) push(sn uint16, pkt []byte) ([]byte, error) {
	diff := int(sn-b.headSN) - 1
	b.headSN = sn

	// invalidate slots if there is a gap in the sequence number
	b.invalidate(b.step, diff)

	// store headSN packet
	off := b.wrap(b.step + diff)
	storedPkt := b.store(off, pkt)

	// for next packet
	b.step = b.wrap(b.step + diff + 1)

	return storedPkt, nil
}

func (b *Bucket) get(sn uint16) ([]byte, error) {
	diff := int(int16(b.headSN - sn))
	if diff < 0 {
		// asking for something ahead of headSN
		return nil, fmt.Errorf("%w, headSN %d, sn %d", ErrPacketTooNew, b.headSN, sn)
	}
	if diff >= b.maxSteps {
		// too old
		return nil, fmt.Errorf("%w, headSN %d, sn %d", ErrPacketTooOld, b.headSN, sn)
	}

	off := b.wrap(b.step - diff - 1)
	slot := b.slots[off]
	cacheSN := binary.BigEndian.Uint16(slot[pktSizeHeader+seqNumOffset:])
	if cacheSN != sn {
		return nil, fmt.Errorf("%w, headSN %d, sn %d, cacheSN %d", ErrPacketMismatch, b.headSN, sn, cacheSN)
	}

	sz := binary.BigEndian.Uint16(slot)
	if sz == invalidPktSize {
		return nil, fmt.Errorf("%w, headSN %d, sn %d, size %d", ErrPacketSizeInvalid, b.headSN, sn, sz)
	}

	off += pktSizeHeader
	return slot[pktSizeHeader : pktSizeHeader+int(sz)], nil
}

func (b *Bucket) set(sn uint16, pkt []byte) ([]byte, error) {
	diff := int(b.headSN - sn)
	if diff >= b.maxSteps {
		return nil, fmt.Errorf("%w, headSN %d, sn %d", ErrPacketTooOld, b.headSN, sn)
	}

	off := b.wrap(b.step - diff - 1)

	// Do not overwrite if duplicate
	if binary.BigEndian.Uint16(b.slots[off][pktSizeHeader+seqNumOffset:]) == sn {
		return nil, ErrRTXPacket
	}

	return b.store(off, pkt), nil
}

func (b *Bucket) store(idx int, pkt []byte) []byte {
	// store packet size
	slot := b.slots[idx]
	binary.BigEndian.PutUint16(slot, uint16(len(pkt)))

	// store packet
	copy(slot[pktSizeHeader:], pkt)

	return slot[pktSizeHeader : pktSizeHeader+len(pkt)]
}

func (b *Bucket) wrap(slot int) int {
	for slot < 0 {
		slot += b.maxSteps
	}

	for slot >= b.maxSteps {
		slot -= b.maxSteps
	}

	return slot
}

func (b *Bucket) invalidate(startSlot int, numSlots int) {
	if numSlots > b.maxSteps {
		numSlots = b.maxSteps
	}

	for i := 0; i < numSlots; i++ {
		off := b.wrap(startSlot + i)
		binary.BigEndian.PutUint16(b.slots[off], invalidPktSize)
	}
}
