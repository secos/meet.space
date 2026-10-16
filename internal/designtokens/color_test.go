package designtokens_test

import (
	"math"
	"regexp"
	"strconv"
	"testing"
)

// Color math for the token tests: WCAG 2 contrast and OKLCH (Bjorn
// Ottosson's OKLab, https://bottosson.github.io/posts/oklab/), the space
// design.md section 4 works in.

var hexRE = regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)

func mustRGB(t *testing.T, hex string) [3]float64 {
	t.Helper()
	if !hexRE.MatchString(hex) {
		t.Fatalf("%q is not a #rrggbb color", hex)
	}
	var rgb [3]float64
	for i := range 3 {
		v, _ := strconv.ParseUint(hex[1+2*i:3+2*i], 16, 8)
		rgb[i] = float64(v) / 255
	}
	return rgb
}

func toLinear(c float64) float64 {
	if c <= 0.04045 {
		return c / 12.92
	}
	return math.Pow((c+0.055)/1.055, 2.4)
}

func luminance(t *testing.T, hex string) float64 {
	rgb := mustRGB(t, hex)
	return 0.2126*toLinear(rgb[0]) + 0.7152*toLinear(rgb[1]) + 0.0722*toLinear(rgb[2])
}

// contrast is the WCAG 2 contrast ratio of two #rrggbb colors.
func contrast(t *testing.T, a, b string) float64 {
	t.Helper()
	la, lb := luminance(t, a), luminance(t, b)
	if la < lb {
		la, lb = lb, la
	}
	return (la + 0.05) / (lb + 0.05)
}

type oklch struct{ L, C, H float64 }

func toOKLCH(t *testing.T, hex string) oklch {
	t.Helper()
	rgb := mustRGB(t, hex)
	r, g, b := toLinear(rgb[0]), toLinear(rgb[1]), toLinear(rgb[2])
	l := math.Cbrt(0.4122214708*r + 0.5363325363*g + 0.0514459929*b)
	m := math.Cbrt(0.2119034982*r + 0.6806995451*g + 0.1073969566*b)
	s := math.Cbrt(0.0883024619*r + 0.2817188376*g + 0.6299787005*b)
	L := 0.2104542553*l + 0.7936177850*m - 0.0040720468*s
	A := 1.9779984951*l - 2.4285922050*m + 0.4505937099*s
	B := 0.0259040371*l + 0.7827717662*m - 0.8086757660*s
	return oklch{L, math.Hypot(A, B), math.Mod(math.Atan2(B, A)*180/math.Pi+360, 360)}
}

// inSRGB reports whether oklch(L C h) is inside the sRGB gamut.
func inSRGB(L, C, h float64) bool {
	A, B := C*math.Cos(h*math.Pi/180), C*math.Sin(h*math.Pi/180)
	l := math.Pow(L+0.3963377774*A+0.2158037573*B, 3)
	m := math.Pow(L-0.1055613458*A-0.0638541728*B, 3)
	s := math.Pow(L-0.0894841775*A-1.2914855480*B, 3)
	const eps = 1e-7
	for _, c := range []float64{
		4.0767416621*l - 3.3077115913*m + 0.2309699292*s,
		-1.2684380046*l + 2.6097574011*m - 0.3413193965*s,
		-0.0041960863*l - 0.7034186147*m + 1.7076147010*s,
	} {
		if c < -eps || c > 1+eps {
			return false
		}
	}
	return true
}

// fittedChroma is design.md 4.1's clipping: the chroma asked for, reduced
// until oklch(L C h) is inside sRGB, with L and h kept.
func fittedChroma(L, C, h float64) float64 {
	if inSRGB(L, C, h) {
		return C
	}
	lo, hi := 0.0, C
	for range 60 {
		mid := (lo + hi) / 2
		if inSRGB(L, mid, h) {
			lo = mid
		} else {
			hi = mid
		}
	}
	return lo
}

// fromOKLCH converts oklch(L C h), which must be inside sRGB, to #rrggbb,
// rounding each channel to the nearest 8-bit value.
func fromOKLCH(L, C, h float64) string {
	A, B := C*math.Cos(h*math.Pi/180), C*math.Sin(h*math.Pi/180)
	l := math.Pow(L+0.3963377774*A+0.2158037573*B, 3)
	m := math.Pow(L-0.1055613458*A-0.0638541728*B, 3)
	s := math.Pow(L-0.0894841775*A-1.2914855480*B, 3)
	out := "#"
	for _, c := range []float64{
		4.0767416621*l - 3.3077115913*m + 0.2309699292*s,
		-1.2684380046*l + 2.6097574011*m - 0.3413193965*s,
		-0.0041960863*l - 0.7034186147*m + 1.7076147010*s,
	} {
		c = min(max(c, 0), 1)
		if c <= 0.0031308 {
			c *= 12.92
		} else {
			c = 1.055*math.Pow(c, 1/2.4) - 0.055
		}
		out += strconv.FormatUint(uint64(math.Round(c*255))|0x100, 16)[1:]
	}
	return out
}

// brandTextFor is design.md 4.3 for one mode: starting from the brand's
// own OKLCH lightness, move L (down in light mode, up in dark) in steps of
// 0.0001, hue unchanged and chroma reduced only as far as sRGB needs
// (4.1's clipping rule), and return the first color that reaches 4.5:1
// against bg. A brand that already reaches it is returned unchanged.
func brandTextFor(t *testing.T, brand, bg string, raise bool) string {
	t.Helper()
	if contrast(t, brand, bg) >= 4.5 {
		return brand
	}
	b := toOKLCH(t, brand)
	step := -0.0001
	if raise {
		step = 0.0001
	}
	for L := b.L; L >= 0 && L <= 1; L += step {
		if hex := fromOKLCH(L, fittedChroma(L, b.C, b.H), b.H); contrast(t, hex, bg) >= 4.5 {
			return hex
		}
	}
	t.Fatalf("no lightness gives %s 4.5:1 on %s", brand, bg)
	return ""
}

// hueDiff is the angle between two hues in degrees, 0..180.
func hueDiff(a, b float64) float64 {
	d := math.Mod(math.Abs(a-b), 360)
	if d > 180 {
		d = 360 - d
	}
	return d
}

// Self-check against published values: white on #2563eb is 5.17:1
// (design.md 4.2 rounds it to 5.2), black on white 21:1, and #2563eb is
// about oklch(0.546 0.215 262.9).
func TestColorMath(t *testing.T) {
	if got := contrast(t, "#ffffff", "#000000"); math.Abs(got-21) > 1e-9 {
		t.Errorf("contrast(white, black) = %v", got)
	}
	if got := contrast(t, "#ffffff", "#2563eb"); math.Abs(got-5.17) > 0.01 {
		t.Errorf("contrast(white, #2563eb) = %v", got)
	}
	c := toOKLCH(t, "#2563eb")
	if math.Abs(c.L-0.546) > 0.001 || math.Abs(c.C-0.215) > 0.001 || math.Abs(c.H-262.88) > 0.01 {
		t.Errorf("oklch(#2563eb) = %+v", c)
	}
	if w := toOKLCH(t, "#ffffff"); math.Abs(w.L-1) > 1e-4 || w.C > 1e-4 {
		t.Errorf("oklch(white) = %+v", w)
	}
	if !inSRGB(0.5, 0, 0) || inSRGB(0.95, 0.2, 262) {
		t.Error("inSRGB is wrong")
	}
	for _, hex := range []string{"#2563eb", "#ffffff", "#000000", "#172033", "#f69656", "#00c7a2"} {
		if c := toOKLCH(t, hex); fromOKLCH(c.L, c.C, c.H) != hex {
			t.Errorf("fromOKLCH(toOKLCH(%s)) = %s", hex, fromOKLCH(c.L, c.C, c.H))
		}
	}
}
