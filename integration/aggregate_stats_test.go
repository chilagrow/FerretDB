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

package integration

import (
	"testing"

	"github.com/stretchr/testify/require"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"

	"github.com/FerretDB/FerretDB/integration/setup"
	"github.com/FerretDB/FerretDB/integration/shareddata"
	"github.com/FerretDB/FerretDB/internal/types"
	"github.com/FerretDB/FerretDB/internal/util/must"
)

func TestAggregateCollStatsCommandErrors(t *testing.T) {
	t.Parallel()

	ctx, collection := setup.Setup(t)

	for name, tc := range map[string]struct { //nolint:vet // used for test only
		command  bson.D          // required, command to run
		database *mongo.Database // defaults to collection.Database()

		err        *mongo.CommandError // required
		altMessage string              // optional, alternative error message
		skip       string              // optional, skip test with a specified reason
	}{
		"NonExistentDatabase": {
			database: collection.Database().Client().Database("non-existent"),
			command: bson.D{
				{"aggregate", collection.Name()},
				{"pipeline", bson.A{bson.D{{"$collStats", bson.D{{"storageStats", bson.D{}}}}}}},
				{"cursor", bson.D{}},
			},
			err: &mongo.CommandError{
				Code: 26,
				Name: "NamespaceNotFound",
				Message: `PlanExecutor error during aggregation :: caused by :: ` +
					`Unable to retrieve storageStats in $collStats stage :: caused by :: ` +
					`Collection [non-existent.TestAggregateCollStatsCommandErrors] not found.`,
			},
			altMessage: "ns not found: non-existent.TestAggregateCollStatsCommandErrors",
		},
		"NonExistentCollection": {
			command: bson.D{
				{"aggregate", "non-existent"},
				{"pipeline", bson.A{bson.D{{"$collStats", bson.D{{"storageStats", bson.D{}}}}}}},
				{"cursor", bson.D{}},
			},
			err: &mongo.CommandError{
				Code: 26,
				Name: "NamespaceNotFound",
				Message: `PlanExecutor error during aggregation :: caused by :: ` +
					`Unable to retrieve storageStats in $collStats stage :: caused by :: ` +
					`Collection [TestAggregateCollStatsCommandErrors.non-existent] not found.`,
			},
			altMessage: "ns not found: TestAggregateCollStatsCommandErrors.non-existent",
		},
	} {
		name, tc := name, tc
		t.Run(name, func(t *testing.T) {
			if tc.skip != "" {
				t.Skip(tc.skip)
			}

			t.Parallel()

			require.NotNil(t, tc.err, "err must not be nil")

			db := tc.database
			if db == nil {
				db = collection.Database()
			}

			var res bson.D
			err := db.RunCommand(ctx, tc.command).Decode(&res)
			AssertEqualAltCommandError(t, *tc.err, tc.altMessage, err)
			require.Nil(t, res)
		})
	}
}

func TestAggregateCollStatsCommandIndexSizes(t *testing.T) {
	t.Parallel()

	ctx, collection := setup.Setup(t, shareddata.DocumentsStrings)

	cursorNoScale, err := collection.Aggregate(ctx, bson.A{
		bson.D{{"$collStats", bson.D{{"storageStats", bson.D{}}}}},
	})
	require.NoError(t, err)

	defer cursorNoScale.Close(ctx)

	scale := int32(1000)
	cursor, err := collection.Aggregate(ctx, bson.A{
		bson.D{{"$collStats", bson.D{{"storageStats", bson.D{{"scale", scale}}}}}},
	})
	require.NoError(t, err)

	defer cursor.Close(ctx)

	resNoScale := FetchAll(t, ctx, cursorNoScale)
	require.Equal(t, 1, len(resNoScale))

	res := FetchAll(t, ctx, cursor)
	require.Equal(t, 1, len(res))

	docNoScale := ConvertDocument(t, resNoScale[0])
	doc := ConvertDocument(t, res[0])

	storageStatsNoScale := must.NotFail(docNoScale.Get("storageStats")).(*types.Document)
	storageStats := must.NotFail(doc.Get("storageStats")).(*types.Document)

	sizeNoScale := must.NotFail(storageStatsNoScale.Get("size")).(int32)
	size := must.NotFail(storageStats.Get("size")).(int32)
	require.Equal(t, sizeNoScale/scale, size)

	avgObjSizeNoScale := must.NotFail(storageStatsNoScale.Get("avgObjSize")).(int32)
	avgObjSize := must.NotFail(storageStats.Get("avgObjSize")).(int32)
	require.Equal(t, avgObjSizeNoScale, avgObjSize)

	storageSizeNoScale := must.NotFail(storageStatsNoScale.Get("storageSize")).(int32)
	storageSize := must.NotFail(storageStats.Get("storageSize")).(int32)
	require.Equal(t, storageSizeNoScale/scale, storageSize)

	freeStorageSizeNoScale := must.NotFail(storageStatsNoScale.Get("freeStorageSize")).(int32)
	freeStorageSize := must.NotFail(storageStats.Get("freeStorageSize")).(int32)
	require.Equal(t, freeStorageSizeNoScale/scale, freeStorageSize)

	totalIndexSizeNoScale := must.NotFail(storageStatsNoScale.Get("totalIndexSize")).(int32)
	totalIndexSize := must.NotFail(storageStats.Get("totalIndexSize")).(int32)
	require.Equal(t, totalIndexSizeNoScale/scale, totalIndexSize)

	totalSizeNoScale := must.NotFail(storageStatsNoScale.Get("totalSize")).(int32)
	totalSize := must.NotFail(storageStats.Get("totalSize")).(int32)
	require.Equal(t, totalSizeNoScale/scale, totalSize)

	indexSizesNoScale := must.NotFail(storageStatsNoScale.Get("indexSizes")).(*types.Document)
	indexSizes := must.NotFail(storageStats.Get("indexSizes")).(*types.Document)

	require.Equal(t, []string{"_id_"}, indexSizesNoScale.Keys())
	require.Equal(t, []string{"_id_"}, indexSizes.Keys())

	for _, index := range indexSizesNoScale.Keys() {
		sizeNoScale := must.NotFail(indexSizesNoScale.Get(index)).(int32)
		size := must.NotFail(indexSizes.Get(index)).(int32)
		require.Equal(t, sizeNoScale/scale, size)
	}
}
