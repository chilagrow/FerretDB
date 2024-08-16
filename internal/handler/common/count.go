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

package common

import (
	"log/slog"

	"github.com/FerretDB/FerretDB/internal/handler/handlerparams"
	"github.com/FerretDB/FerretDB/internal/types"
)

// CountParams represents parameters for the count command.
type CountParams struct {
	Filter     *types.Document `ferretdb:"query,opt"`
	DB         string          `ferretdb:"$db"`
	Collection string          `ferretdb:"count,collection"`

	Skip  int64 `ferretdb:"skip,opt,positiveNumber"`
	Limit int64 `ferretdb:"limit,opt,positiveNumber"`

	Collation *types.Document `ferretdb:"collation,unimplemented"`

	Fields any `ferretdb:"fields,ignored"` // legacy MongoDB shell adds it, but it is never actually used

	APIVersion           string `ferretdb:"apiVersion,ignored"`
	APIString            string `ferretdb:"apiStrict,ignored"`
	APIDeprecationErrors bool   `ferretdb:"apiDeprecationErrors,ignored"`

	MaxTimeMS      int64           `ferretdb:"maxTimeMS,ignored"`
	Hint           any             `ferretdb:"hint,ignored"`
	ReadConcern    *types.Document `ferretdb:"readConcern,ignored"`
	Comment        string          `ferretdb:"comment,ignored"`
	LSID           any             `ferretdb:"lsid,ignored"`
	ClusterTime    any             `ferretdb:"$clusterTime,ignored"`
	ReadPreference *types.Document `ferretdb:"$readPreference,ignored"`
}

// GetCountParams returns the parameters for the count command.
func GetCountParams(document *types.Document, l *slog.Logger) (*CountParams, error) {
	var count CountParams

	err := handlerparams.ExtractParams(document, "count", &count, l)
	if err != nil {
		return nil, err
	}

	return &count, nil
}
