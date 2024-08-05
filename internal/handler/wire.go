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

package handler

import (
	"github.com/FerretDB/wire"

	"github.com/FerretDB/FerretDB/internal/bson"
	"github.com/FerretDB/FerretDB/internal/types"
	"github.com/FerretDB/FerretDB/internal/util/lazyerrors"
)

// init disallows NaN values by setting false to [wire.AllowNaN].
func init() {
	wire.AllowNaN = false
}

// opMsgDocument gets a raw document from section 0 and converts to [*types.Document].
// Then it iterates raw documents from sections 1 if any, appends them
// to the response using the section identifier as the key.
func opMsgDocument(msg *wire.OpMsg) (*types.Document, error) {
	res, err := bson.TypesDocument(msg.RawSection0())
	if err != nil {
		return nil, lazyerrors.Error(err)
	}

	for _, section := range msg.Sections() {
		if section.Kind == 0 {
			continue
		}

		docs := section.Documents()
		a := types.MakeArray(len(docs))

		for _, d := range docs {
			var doc *types.Document

			if doc, err = bson.TypesDocument(d); err != nil {
				return nil, lazyerrors.Error(err)
			}

			a.Append(doc)
		}

		res.Set(section.Identifier, a)
	}

	return res, nil
}
