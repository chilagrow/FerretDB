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

package stages

import (
	"context"

	"github.com/FerretDB/FerretDB/internal/handlers/common"
	"github.com/FerretDB/FerretDB/internal/handlers/commonerrors"
	"github.com/FerretDB/FerretDB/internal/types"
	"github.com/FerretDB/FerretDB/internal/util/iterator"
)

// match represents $match stage.
type match struct {
	filter *types.Document
}

// newMatch creates a new $match stage.
func newMatch(stage *types.Document) (Stage, error) {
	filter, err := common.GetRequiredParam[*types.Document](stage, "$match")
	if err != nil {
		return nil, commonerrors.NewCommandErrorMsgWithArgument(
			commonerrors.ErrMatchBadExpression,
			"the match filter must be an expression in an object",
			"$match (stage)",
		)
	}

	return &match{
		filter: filter,
	}, nil
}

// Process implements Stage interface.
func (m *match) Process(ctx context.Context, iter types.DocumentsIterator, closer *iterator.MultiCloser) (types.DocumentsIterator, error) {
	return common.FilterIterator(iter, closer, m.filter), nil
}

// Type  implements Stage interface.
func (m *match) Type() StageType {
	return StageTypeDocuments
}

// check interfaces
var (
	_ Stage = (*match)(nil)
)