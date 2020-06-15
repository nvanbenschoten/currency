// Copyright (c) 2020 Bojan Zivanovic and contributors
// SPDX-License-Identifier: MIT

package currency

import (
	"bytes"
	"encoding/json"
	"fmt"

	"github.com/ericlagergren/decimal"
)

// RoundingMode determines how the amount will be rounded.
type RoundingMode uint8

const (
	// RoundHalfUp rounds up if the next digit is >= 5.
	RoundHalfUp RoundingMode = iota
	// RoundHalfDown rounds up if the next digit is > 5.
	RoundHalfDown
	// RoundUp rounds away from 0.
	RoundUp
	// RoundDown rounds towards 0, truncating extra digits.
	RoundDown
)

// InvalidNumberError is returned when a numeric string can't be converted to a decimal.
type InvalidNumberError struct {
	Op     string
	Number string
}

func (e InvalidNumberError) Error() string {
	return fmt.Sprintf("currency/%v: invalid number %q", e.Op, e.Number)
}

// InvalidCurrencyCodeError is returned when a currency code is invalid or unrecognized.
type InvalidCurrencyCodeError struct {
	Op           string
	CurrencyCode string
}

func (e InvalidCurrencyCodeError) Error() string {
	return fmt.Sprintf("currency/%v: invalid currency code %q", e.Op, e.CurrencyCode)
}

// MismatchError is returned when two amounts have mismatched currency codes.
type MismatchError struct {
	Op string
	A  Amount
	B  Amount
}

func (e MismatchError) Error() string {
	return fmt.Sprintf("currency/%v: %q and %q have mismatched currency codes", e.Op, e.A, e.B)
}

// Amount stores a decimal number with its currency code.
type Amount struct {
	number       *decimal.Big
	currencyCode string
}

// NewAmount creates a new Amount from a numeric string and a currency code.
func NewAmount(n, currencyCode string) (Amount, error) {
	number, ok := new(decimal.Big).SetString(n)
	if !ok || number.IsNaN(0) {
		return Amount{}, InvalidNumberError{"NewAmount", n}
	}
	if !IsValid(currencyCode) {
		return Amount{}, InvalidCurrencyCodeError{"NewAmount", currencyCode}
	}

	return Amount{number, currencyCode}, nil
}

// Number returns the number as a numeric string.
func (a Amount) Number() string {
	if a.number == nil {
		return "0"
	}
	return a.number.String()
}

// CurrencyCode returns the currency code.
func (a Amount) CurrencyCode() string {
	return a.currencyCode
}

// String returns the string representation of a.
func (a Amount) String() string {
	return a.Number() + " " + a.CurrencyCode()
}

// ToMinorUnits returns a in minor units.
func (a Amount) ToMinorUnits() int64 {
	if a.number == nil {
		return 0
	}
	result, _ := a.Round().number.SetScale(0).Int64()

	return result
}

// Convert converts a to a different currency.
func (a Amount) Convert(currencyCode, rate string) (Amount, error) {
	if !IsValid(currencyCode) {
		return Amount{}, InvalidCurrencyCodeError{"Amount.Convert", currencyCode}
	}
	result, ok := new(decimal.Big).SetString(rate)
	if !ok || result.IsNaN(0) {
		return Amount{}, InvalidNumberError{"Amount.Convert", rate}
	}
	result.Mul(a.number, result)

	return Amount{result, currencyCode}, nil
}

// Add adds a and b together and returns the result.
func (a Amount) Add(b Amount) (Amount, error) {
	if a.currencyCode != b.currencyCode {
		return Amount{}, MismatchError{"Amount.Add", a, b}
	}
	result := new(decimal.Big)
	result.Add(a.number, b.number)

	return Amount{result, a.currencyCode}, nil
}

// Sub subtracts b from a and returns the result.
func (a Amount) Sub(b Amount) (Amount, error) {
	if a.currencyCode != b.currencyCode {
		return Amount{}, MismatchError{"Amount.Sub", a, b}
	}
	result := new(decimal.Big)
	result.Sub(a.number, b.number)

	return Amount{result, a.currencyCode}, nil
}

// Mul multiplies a by n and returns the result.
func (a Amount) Mul(n string) (Amount, error) {
	result, ok := new(decimal.Big).SetString(n)
	if !ok || result.IsNaN(0) {
		return Amount{}, InvalidNumberError{"Amount.Mul", n}
	}
	result.Mul(a.number, result)

	return Amount{result, a.currencyCode}, nil
}

// Div divides a by n and returns the result.
func (a Amount) Div(n string) (Amount, error) {
	result, ok := new(decimal.Big).SetString(n)
	if !ok || result.IsNaN(0) || result.Sign() == 0 {
		return Amount{}, InvalidNumberError{"Amount.Div", n}
	}
	result.Quo(a.number, result)

	return Amount{result, a.currencyCode}, nil
}

// Round is a shortcut for RoundTo(currency.DefaultDigits, currency.RoundHalfUp).
func (a Amount) Round() Amount {
	return a.RoundTo(DefaultDigits, RoundHalfUp)
}

// RoundTo rounds a to the given number of fraction digits.
func (a Amount) RoundTo(digits uint8, mode RoundingMode) Amount {
	if digits == DefaultDigits {
		digits, _ = GetDigits(a.currencyCode)
	}
	extModes := map[RoundingMode]decimal.RoundingMode{
		RoundHalfUp:   decimal.ToNearestAway,
		RoundHalfDown: decimal.ToNearestTowardZero,
		RoundUp:       decimal.AwayFromZero,
		RoundDown:     decimal.ToZero,
	}
	result := new(decimal.Big).Copy(a.number)
	result.Context.RoundingMode = extModes[mode]
	result.Quantize(int(digits))

	return Amount{result, a.currencyCode}
}

// Cmp compares a and b and returns:
//
//   -1 if a <  b
//    0 if a == b
//   +1 if a >  b
//
func (a Amount) Cmp(b Amount) (int, error) {
	if a.currencyCode != b.currencyCode {
		return -1, MismatchError{"Amount.Cmp", a, b}
	}
	return a.number.Cmp(b.number), nil
}

// Equal returns whether a and b are equal.
func (a Amount) Equal(b Amount) bool {
	if a.currencyCode != b.currencyCode {
		return false
	}
	return a.number.Cmp(b.number) == 0
}

// IsPositive returns whether a is positive.
func (a Amount) IsPositive() bool {
	return a.number.Sign() == 1
}

// IsNegative returns whether a is negative.
func (a Amount) IsNegative() bool {
	return a.number.Sign() == -1
}

// IsZero returns whether a is zero.
func (a Amount) IsZero() bool {
	return a.number.Sign() == 0
}

// MarshalBinary implements the encoding.BinaryMarshaler interface.
func (a Amount) MarshalBinary() ([]byte, error) {
	buf := bytes.Buffer{}
	buf.WriteString(a.CurrencyCode())
	buf.WriteString(a.Number())

	return buf.Bytes(), nil
}

// UnmarshalBinary implements the encoding.BinaryUnmarshaler interface.
func (a *Amount) UnmarshalBinary(data []byte) error {
	if len(data) < 3 {
		return InvalidCurrencyCodeError{"Amount.UnmarshalBinary", string(data)}
	}
	n := string(data[3:])
	currencyCode := string(data[0:3])
	number, ok := decimal.WithContext(decimal.Context64).SetString(n)
	if !ok || number.IsNaN(0) {
		return InvalidNumberError{"Amount.UnmarshalBinary", n}
	}
	if !IsValid(currencyCode) {
		return InvalidCurrencyCodeError{"Amount.UnmarshalBinary", currencyCode}
	}
	a.number = number
	a.currencyCode = currencyCode

	return nil
}

// MarshalJSON implements the json.Marshaler interface.
func (a Amount) MarshalJSON() ([]byte, error) {
	return json.Marshal(&struct {
		Number       string `json:"number"`
		CurrencyCode string `json:"currency"`
	}{
		Number:       a.Number(),
		CurrencyCode: a.CurrencyCode(),
	})
}

// UnmarshalJSON implements the json.Unmarshaler interface.
func (a *Amount) UnmarshalJSON(data []byte) error {
	aux := struct {
		Number       string `json:"number"`
		CurrencyCode string `json:"currency"`
	}{}
	err := json.Unmarshal(data, &aux)
	if err != nil {
		return err
	}
	number, ok := new(decimal.Big).SetString(aux.Number)
	if !ok || number.IsNaN(0) {
		return InvalidNumberError{"Amount.UnmarshalJSON", aux.Number}
	}
	if !IsValid(aux.CurrencyCode) {
		return InvalidCurrencyCodeError{"Amount.UnmarshalJSON", aux.CurrencyCode}
	}
	a.number = number
	a.currencyCode = aux.CurrencyCode

	return nil
}
