package netdiag

import "math"

var nan = math.NaN()

// Clean remplace les NaN (paquets perdus) par -1 pour la sérialisation JSON.
func Clean(v []float64) []float64 {
	out := make([]float64, len(v))
	for i, x := range v {
		if math.IsNaN(x) {
			out[i] = -1
		} else {
			out[i] = x
		}
	}
	return out
}
