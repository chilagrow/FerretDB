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
	"context"
	"fmt"

	"github.com/FerretDB/wire"

	"github.com/FerretDB/FerretDB/internal/backends"
	"github.com/FerretDB/FerretDB/internal/bson"
	"github.com/FerretDB/FerretDB/internal/handler/common"
	"github.com/FerretDB/FerretDB/internal/handler/handlererrors"
	"github.com/FerretDB/FerretDB/internal/types"
	"github.com/FerretDB/FerretDB/internal/util/lazyerrors"
	"github.com/FerretDB/FerretDB/internal/util/must"
)

// MsgValidate implements `validate` command.
//
// The passed context is canceled when the client connection is closed.
func (h *Handler) MsgValidate(connCtx context.Context, msg *wire.OpMsg) (*wire.OpMsg, error) {
	document, err := bson.Section0Document(msg)
	if err != nil {
		return nil, lazyerrors.Error(err)
	}

	common.Ignored(document, h.L, "full", "repair", "metadata", "checkBSONConformance")

	command := document.Command()

	dbName, err := common.GetRequiredParam[string](document, "$db")
	if err != nil {
		return nil, err
	}

	collection, err := common.GetRequiredParam[string](document, command)
	if err != nil {
		return nil, err
	}

	db, err := h.b.Database(dbName)
	if err != nil {
		return nil, lazyerrors.Error(err)
	}

	c, err := db.Collection(collection)
	if err != nil {
		return nil, lazyerrors.Error(err)
	}

	stats, err := c.Stats(connCtx, &backends.CollectionStatsParams{Refresh: true})
	if err != nil {
		if backends.ErrorCodeIs(err, backends.ErrorCodeCollectionDoesNotExist) {
			msg := fmt.Sprintf("Collection '%s.%s' does not exist to validate.", dbName, collection)
			return nil, handlererrors.NewCommandErrorMsgWithArgument(handlererrors.ErrNamespaceNotFound, msg, document.Command())
		}

		return nil, lazyerrors.Error(err)
	}

	// TODO https://github.com/FerretDB/FerretDB/issues/3841
	return bson.NewOpMsg(
		must.NotFail(types.NewDocument(
			"ns", dbName+"."+collection,
			"nInvalidDocuments", int32(0),
			"nNonCompliantDocuments", int32(0),
			"nrecords", int32(stats.CountDocuments),
			"nIndexes", int32(len(stats.IndexSizes)),
			"valid", true,
			"repaired", false,
			"warnings", types.MakeArray(0),
			"errors", types.MakeArray(0),
			"extraIndexEntries", types.MakeArray(0),
			"missingIndexEntries", types.MakeArray(0),
			"corruptRecords", types.MakeArray(0),
			"ok", float64(1),
		)),
	)
}
