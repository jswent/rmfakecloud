package templates

import (
	"math"
	"regexp"
	"sort"
	"strconv"
)

// segment is a stroked straight line found in a content stream, in PDF user
// space (points, y up).
type segment struct {
	x0, y0, x1, y1 float64
	width          float64 // stroke width in points
	grey           float64
}

type matrix [6]float64

func (m matrix) mul(n matrix) matrix { // m then n
	return matrix{
		m[0]*n[0] + m[1]*n[2], m[0]*n[1] + m[1]*n[3],
		m[2]*n[0] + m[3]*n[2], m[2]*n[1] + m[3]*n[3],
		m[4]*n[0] + m[5]*n[2] + n[4], m[4]*n[1] + m[5]*n[3] + n[5],
	}
}

func (m matrix) apply(x, y float64) (float64, float64) {
	return x*m[0] + y*m[2] + m[4], x*m[1] + y*m[3] + m[5]
}

var tokenRE = regexp.MustCompile(`/[^\s/\[\]()<>]+|\[[^\]]*\]|-?\d*\.?\d+|[A-Za-z*']+`)

// greyRules returns the single-segment stroked lines drawn in a neutral
// grey between 0.6 and 0.9: the template rules of the tablet's PDF export
// (its ink is black or coloured, and drawn as filled outlines). It
// understands the operators the tablet and Render write: q Q cm w G g RG
// SCN m l S re n.
func greyRules(content []byte) []segment {
	type state struct {
		ctm       matrix
		grey      float64
		greyOK    bool
		lineWidth float64
	}
	st := state{ctm: matrix{1, 0, 0, 1, 0, 0}, lineWidth: 1}
	var stack []state
	var nums []float64
	type pt struct{ x, y float64 }
	var path [][]pt
	var out []segment
	for _, t := range tokenRE.FindAllString(string(content), -1) {
		if v, err := strconv.ParseFloat(t, 64); err == nil {
			nums = append(nums, v)
			continue
		}
		last := func(n int) []float64 {
			if len(nums) < n {
				return make([]float64, n)
			}
			return nums[len(nums)-n:]
		}
		switch t {
		case "q":
			stack = append(stack, st)
		case "Q":
			if len(stack) > 0 {
				st, stack = stack[len(stack)-1], stack[:len(stack)-1]
			}
		case "cm":
			a := last(6)
			st.ctm = matrix{a[0], a[1], a[2], a[3], a[4], a[5]}.mul(st.ctm)
		case "w":
			st.lineWidth = last(1)[0]
		case "G":
			st.grey, st.greyOK = last(1)[0], true
		case "RG", "SCN":
			if len(nums) >= 3 {
				c := last(3)
				st.grey, st.greyOK = c[0], c[0] == c[1] && c[1] == c[2]
			} else {
				st.greyOK = false
			}
		case "m":
			a := last(2)
			path = append(path, []pt{{a[0], a[1]}})
		case "l":
			if len(path) > 0 {
				a := last(2)
				path[len(path)-1] = append(path[len(path)-1], pt{a[0], a[1]})
			}
		case "S":
			if st.greyOK && st.grey > 0.6 && st.grey < 0.9 {
				for _, sp := range path {
					if len(sp) != 2 {
						continue
					}
					x0, y0 := st.ctm.apply(sp[0].x, sp[0].y)
					x1, y1 := st.ctm.apply(sp[1].x, sp[1].y)
					w := st.lineWidth * math.Hypot(st.ctm[0], st.ctm[1])
					out = append(out, segment{x0, y0, x1, y1, w, st.grey})
				}
			}
			path = nil
		case "n", "f", "F", "f*", "B", "B*", "b", "b*", "s":
			path = nil
		}
		nums = nums[:0]
	}
	return out
}

// horizontal returns the horizontal segments, top of the page first.
func horizontal(segs []segment) []segment {
	var out []segment
	for _, s := range segs {
		if math.Abs(s.y0-s.y1) < 1e-6 {
			if s.x0 > s.x1 {
				s.x0, s.x1 = s.x1, s.x0
			}
			out = append(out, s)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].y0 > out[j].y0 })
	return out
}
