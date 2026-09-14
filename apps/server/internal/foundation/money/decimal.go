package money

import (
	"errors"
	"math/big"
	"strings"
)

// Scale 是 Decimal 的定点小数位，与存储上限 NUMERIC(18,4) 一致。
const Scale = 4

var scaleFactor = big.NewInt(10_000)

// Decimal 是按 4 位小数定点的精确金额，用于退款汇总与开支统计等派生计算；
// 传输时仍用 Canonicalize 生成的字符串。零值即 0。
type Decimal struct {
	units *big.Int
}

// ErrInvalidDecimal 表示输入不是可带负号的十进制数字串或小数位超过 4 位。
var ErrInvalidDecimal = errors.New("money: invalid decimal")

// Zero 返回 0。
func Zero() Decimal { return Decimal{units: new(big.Int)} }

// ParseDecimal 解析可带负号的十进制字符串（"128.50"、"-3.5000"）；小数位最多 4 位。
func ParseDecimal(s string) (Decimal, error) {
	neg := false
	if strings.HasPrefix(s, "-") {
		neg = true
		s = s[1:]
	}
	if !isUnsignedDecimal(s) {
		return Decimal{}, ErrInvalidDecimal
	}
	intPart, fracPart, _ := strings.Cut(s, ".")
	if len(fracPart) > Scale {
		return Decimal{}, ErrInvalidDecimal
	}
	fracPart += strings.Repeat("0", Scale-len(fracPart))
	units, ok := new(big.Int).SetString(intPart+fracPart, 10)
	if !ok {
		return Decimal{}, ErrInvalidDecimal
	}
	if neg {
		units.Neg(units)
	}
	return Decimal{units: units}, nil
}

// MustDecimal 解析已知合法的字符串，失败时 panic；仅用于常量与测试。
func MustDecimal(s string) Decimal {
	d, err := ParseDecimal(s)
	if err != nil {
		panic(err)
	}
	return d
}

func (d Decimal) value() *big.Int {
	if d.units == nil {
		return new(big.Int)
	}
	return d.units
}

// Add 返回 d + o。
func (d Decimal) Add(o Decimal) Decimal {
	return Decimal{units: new(big.Int).Add(d.value(), o.value())}
}

// Sub 返回 d − o。
func (d Decimal) Sub(o Decimal) Decimal {
	return Decimal{units: new(big.Int).Sub(d.value(), o.value())}
}

// Cmp 比较：-1、0、1。
func (d Decimal) Cmp(o Decimal) int { return d.value().Cmp(o.value()) }

// Sign 返回符号：-1、0、1。
func (d Decimal) Sign() int { return d.value().Sign() }

// IsZero 判断是否为 0。
func (d Decimal) IsZero() bool { return d.value().Sign() == 0 }

// Ratio 返回 d / o 的浮点近似（o 为 0 时返回 0），仅供展示占比。
func (d Decimal) Ratio(o Decimal) float64 {
	if o.IsZero() {
		return 0
	}
	f, _ := new(big.Rat).SetFrac(d.value(), o.value()).Float64()
	return f
}

// Format 按币种小数位输出带符号的规范字符串（"-12.50"、"0"）。
// 若第 minorUnits 位之后仍有非零数字（不应出现在同币种金额的加减结果中），保留到 4 位。
func (d Decimal) Format(minorUnits int) string {
	if minorUnits < 0 {
		minorUnits = 0
	}
	if minorUnits > Scale {
		minorUnits = Scale
	}
	v := d.value()
	neg := v.Sign() < 0
	abs := new(big.Int).Abs(v)
	intPart, fracPart := new(big.Int).QuoRem(abs, scaleFactor, new(big.Int))
	frac := fracPart.String()
	frac = strings.Repeat("0", Scale-len(frac)) + frac
	trimmed := strings.TrimRight(frac, "0")
	if len(trimmed) > minorUnits {
		minorUnits = len(trimmed)
	}
	out := intPart.String()
	if minorUnits > 0 {
		out += "." + frac[:minorUnits]
	}
	if neg {
		out = "-" + out
	}
	return out
}
