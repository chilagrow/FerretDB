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

package bson

import (
	"github.com/FerretDB/wire/wirebson"

	"github.com/FerretDB/FerretDB/internal/types"
	"github.com/FerretDB/FerretDB/internal/util/lazyerrors"
)

// Array represents a BSON array in the (partially) decoded form.
type Array struct {
	*wirebson.Array // embed to delegate method
}

// TypesArray gets an array, decodes and converts to [*types.Array].
func TypesArray(arr wirebson.AnyArray) (*types.Array, error) {
	wArr, err := arr.Decode()
	if err != nil {
		return nil, lazyerrors.Error(err)
	}

	bArr := &Array{Array: wArr}

	tArr, err := bArr.Convert()
	if err != nil {
		return nil, lazyerrors.Error(err)
	}

	return tArr, nil
}
