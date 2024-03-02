/*
 * Cadence - The resource-oriented smart contract programming language
 *
 * Copyright Dapper Labs, Inc.
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *   http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

package main

import (
	"encoding/json"
	"sort"

	jsoncdc "github.com/onflow/cadence/encoding/json"
	"github.com/onflow/cadence/runtime"
	"github.com/onflow/cadence/runtime/interpreter"
)

type Value interface {
	isValue()
}

// TypeOnlyValue

type FallbackValue struct {
	Type        any    `json:"type"`
	Description string `json:"description"`
}

var _ Value = FallbackValue{}

func (FallbackValue) isValue() {}

func (v FallbackValue) MarshalJSON() ([]byte, error) {
	type Alias FallbackValue
	return json.Marshal(&struct {
		Kind string `json:"kind"`
		Alias
	}{
		Kind:  "fallback",
		Alias: (Alias)(v),
	})
}

// PrimitiveValue

type PrimitiveValue struct {
	Type  any             `json:"type"`
	Value json.RawMessage `json:"value"`
}

var _ Value = PrimitiveValue{}

func (PrimitiveValue) isValue() {}

func (v PrimitiveValue) MarshalJSON() ([]byte, error) {
	type Alias PrimitiveValue
	return json.Marshal(&struct {
		Kind string `json:"kind"`
		Alias
	}{
		Kind:  "primitive",
		Alias: (Alias)(v),
	})
}

// DictionaryValue

type DictionaryValue struct {
	Type any     `json:"type"`
	Keys []Value `json:"keys"`
}

var _ Value = DictionaryValue{}

func (DictionaryValue) isValue() {}

func (v DictionaryValue) MarshalJSON() ([]byte, error) {
	type Alias DictionaryValue
	return json.Marshal(&struct {
		Kind string `json:"kind"`
		Alias
	}{
		Kind:  "dictionary",
		Alias: (Alias)(v),
	})
}

// ArrayValue

type ArrayValue struct {
	Type  any `json:"type"`
	Count int `json:"count"`
}

var _ Value = ArrayValue{}

func (ArrayValue) isValue() {}

func (v ArrayValue) MarshalJSON() ([]byte, error) {
	type Alias ArrayValue
	return json.Marshal(&struct {
		Kind string `json:"kind"`
		Alias
	}{
		Kind:  "array",
		Alias: (Alias)(v),
	})
}

// CompositeValue

type CompositeValue struct {
	Type   any      `json:"type"`
	Fields []string `json:"fields"`
}

var _ Value = CompositeValue{}

func (CompositeValue) isValue() {}

func (v CompositeValue) MarshalJSON() ([]byte, error) {
	type Alias CompositeValue
	return json.Marshal(&struct {
		Kind string `json:"kind"`
		Alias
	}{
		Kind:  "composite",
		Alias: (Alias)(v),
	})
}

// SomeValue

type SomeValue struct {
	Type  any   `json:"type"`
	Value Value `json:"value"`
}

var _ Value = SomeValue{}

func (SomeValue) isValue() {}

func (v SomeValue) MarshalJSON() ([]byte, error) {
	type Alias SomeValue
	return json.Marshal(&struct {
		Kind string `json:"kind"`
		Alias
	}{
		Kind:  "some",
		Alias: (Alias)(v),
	})
}

// prepareValue

var pathLinkValueFieldNames = []string{"targetPath", "type"}
var publishedValueFieldNames = []string{"recipient", "type"}

// TODO:
//   - AccountCapabilityControllerValue
//   - StorageCapabilityControllerValue
//   - PathCapabilityValue
func prepareValue(value interpreter.Value, inter *interpreter.Interpreter) (Value, error) {
	ty := prepareType(value, inter)

	switch value := value.(type) {
	case interpreter.BoolValue,
		interpreter.NumberValue,
		*interpreter.StringValue,
		interpreter.CharacterValue,
		interpreter.AddressValue,
		interpreter.PathValue,
		interpreter.TypeValue,
		*interpreter.IDCapabilityValue:

		exported, err := runtime.ExportValue(value, inter, interpreter.EmptyLocationRange)
		if err != nil {
			return nil, err
		}

		exportedJSON, err := jsoncdc.Encode(exported)
		if err != nil {
			return nil, err
		}

		return PrimitiveValue{
			Type:  ty,
			Value: exportedJSON,
		}, nil

	case *interpreter.DictionaryValue:
		keys := make([]Value, 0, value.Count())

		var err error

		value.IterateKeys(inter, func(key interpreter.Value) (resume bool) {
			var preparedKey Value
			preparedKey, err = prepareValue(key, inter)
			if err != nil {
				return false
			}

			keys = append(keys, preparedKey)

			return true
		})

		if err != nil {
			return nil, err
		}

		return DictionaryValue{
			Type: ty,
			Keys: keys,
		}, nil

	case *interpreter.CompositeValue:
		fields := make([]string, 0, value.FieldCount())

		value.ForEachFieldName(func(field string) (resume bool) {
			fields = append(fields, field)

			return true
		})

		sort.Strings(fields)

		return CompositeValue{
			Type:   ty,
			Fields: fields,
		}, nil

	case *interpreter.SimpleCompositeValue:
		fieldNames := value.FieldNames

		fields := make([]string, 0, len(fieldNames))
		copy(fields, fieldNames)

		sort.Strings(fields)

		return CompositeValue{
			Type:   ty,
			Fields: fields,
		}, nil

	case interpreter.PathLinkValue: //nolint:staticcheck
		return CompositeValue{
			Type:   ty,
			Fields: pathLinkValueFieldNames,
		}, nil

	case interpreter.AccountLinkValue: //nolint:staticcheck
		return CompositeValue{
			Type: ty,
		}, nil

	case *interpreter.PublishedValue:
		return CompositeValue{
			Type:   ty,
			Fields: publishedValueFieldNames,
		}, nil

	case *interpreter.ArrayValue:
		return ArrayValue{
			Type:  ty,
			Count: value.Count(),
		}, nil

	case *interpreter.SomeValue:
		innerValue := value.InnerValue(inter, interpreter.EmptyLocationRange)

		preparedInnerValue, err := prepareValue(innerValue, inter)
		if err != nil {
			return nil, err
		}

		return SomeValue{
			Type:  ty,
			Value: preparedInnerValue,
		}, nil

	default:
		return FallbackValue{
			Type:        ty,
			Description: value.String(),
		}, nil
	}
}
