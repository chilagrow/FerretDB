// Copyright 2021 FerretDB Inc.
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

package operation

import (
	"cmp"
	"context"
	"errors"
	"log/slog"
	"slices"
	"sync"
	"sync/atomic"

	"github.com/FerretDB/wire/wirebson"
	"golang.org/x/exp/maps"

	"github.com/FerretDB/FerretDB/v2/internal/util/resource"
)

// Registry stores operations.
type Registry struct {
	rw         sync.RWMutex
	operations map[int32]*Operation

	nextOperationID atomic.Int32

	token *resource.Token

	l *slog.Logger
}

// NewRegistry creates a new operation registry.
func NewRegistry(l *slog.Logger) *Registry {
	res := &Registry{
		operations: map[int32]*Operation{},
		token:      resource.NewToken(),
		l:          l,
	}

	resource.Track(res, res.token)

	return res
}

// Start starts a new operation and returns the operation ID.
func (r *Registry) Start(ctx context.Context, op string) (context.Context, int32) {
	ctx, cancel := context.WithCancelCause(ctx)
	id := r.nextOperationID.Add(1)
	o := newOperation(id, op, cancel)

	r.rw.Lock()
	defer r.rw.Unlock()

	r.operations[id] = o

	return ctx, id
}

// Stop ends an operation.
func (r *Registry) Stop(id int32) {
	r.rw.Lock()
	defer r.rw.Unlock()

	o := r.operations[id]

	delete(r.operations, id)

	o.close()
}

// Kill kills an operation by canceling the context.
// It does nothing if the operation does not exist.
func (r *Registry) Kill(opid int32) {
	r.rw.Lock()
	defer r.rw.Unlock()

	if op, ok := r.operations[opid]; ok {
		op.cancel(errors.New("kill operation"))

		r.l.Debug("Operation killed", slog.Int("opid", int(opid)))
	}
}

// Update sets additional information of the given operation.
//
// If the operation does not exist, it does nothing.
func (r *Registry) Update(id int32, db, collection string, command *wirebson.Document) {
	r.rw.Lock()
	defer r.rw.Unlock()

	o, ok := r.operations[id]
	if !ok {
		return
	}

	o.DB = db
	o.Collection = collection
	o.Command = command
}

// Operations returns all operations.
func (r *Registry) Operations() []Operation {
	r.rw.RLock()
	defer r.rw.RUnlock()

	ops := maps.Values(r.operations)

	slices.SortFunc(ops, func(a, b *Operation) int {
		return cmp.Compare(a.OpID, b.OpID)
	})

	var res []Operation
	for _, op := range ops {
		res = append(res, *op)
	}

	return res
}

// Close closes the operation registry.
func (r *Registry) Close() {
	r.rw.Lock()
	defer r.rw.Unlock()

	for _, o := range r.operations {
		o.close()
	}

	r.operations = nil

	resource.Untrack(r, r.token)
}
