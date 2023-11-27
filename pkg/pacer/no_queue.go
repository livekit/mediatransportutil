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

package pacer

import (
	"sync"

	"github.com/gammazero/deque"
	"github.com/livekit/protocol/logger"
)

type NoQueue struct {
	*Base

	logger logger.Logger

	lock      sync.RWMutex
	packets   deque.Deque[*Packet]
	wake      chan struct{}
	isStopped bool
}

func NewNoQueue(logger logger.Logger) *NoQueue {
	n := &NoQueue{
		Base:   NewBase(logger),
		logger: logger,
		wake:   make(chan struct{}, 1),
	}
	n.packets.SetMinCapacity(9)

	return n
}

func (n *NoQueue) Start() {
	n.lock.Lock()
	defer n.lock.Unlock()
	if n.isStopped {
		return
	}
	go n.sendWorker()
}

func (n *NoQueue) Stop() {
	n.lock.Lock()
	if n.isStopped {
		n.lock.Unlock()
		return
	}

	close(n.wake)
	n.isStopped = true
	n.lock.Unlock()
}

func (n *NoQueue) Enqueue(p *Packet) {
	n.lock.Lock()
	defer n.lock.Unlock()

	n.packets.PushBack(p)
	if n.packets.Len() == 1 && !n.isStopped {
		select {
		case n.wake <- struct{}{}:
		default:
		}
	}
}

func (n *NoQueue) sendWorker() {
	for {
		<-n.wake
		for {
			n.lock.Lock()
			if n.isStopped {
				n.lock.Unlock()
				return
			}

			if n.packets.Len() == 0 {
				n.lock.Unlock()
				break
			}
			p := n.packets.PopFront()
			n.lock.Unlock()

			n.Base.SendPacket(p)
		}
	}
}

// ------------------------------------------------
