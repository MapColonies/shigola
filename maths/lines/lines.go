package lines

import "github.com/MapColonies/shigola"

func FromTLineString(lnstr shigola.LineString) (ln [][2]float64) {
	pts := lnstr.Subpoints()
	for i := range pts {
		ln = append(ln, [2]float64{pts[i].X(), pts[i].Y()})
	}
	return ln
}
