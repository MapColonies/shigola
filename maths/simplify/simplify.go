package simplify

import (
	"github.com/MapColonies/shigola"
	"github.com/MapColonies/shigola/basic"
	"github.com/MapColonies/shigola/maths"
	"github.com/MapColonies/shigola/maths/points"
)

// SimplifyGeometry applies the DouglasPeucker simplification routine to the supplied geometry
func SimplifyGeometry(g shigola.Geometry, tolerance float64) shigola.Geometry {
	switch gg := g.(type) {
	case shigola.Polygon:
		return simplifyPolygon(gg, tolerance)

	case shigola.MultiPolygon:
		var newMP basic.MultiPolygon

		for _, p := range gg.Polygons() {
			sp := simplifyPolygon(p, tolerance)
			if sp == nil {
				continue
			}
			newMP = append(newMP, sp)
		}

		if len(newMP) == 0 {
			return nil
		}

		return newMP

	case shigola.LineString:
		return simplifyLineString(gg, tolerance)

	case shigola.MultiLine:
		var newML basic.MultiLine

		for _, l := range gg.Lines() {
			sl := simplifyLineString(l, tolerance)
			if sl == nil {
				continue
			}
			newML = append(newML, sl)
		}

		if len(newML) == 0 {
			return nil
		}

		return newML
	}

	return g
}

func simplifyLineString(g shigola.LineString, tolerance float64) basic.Line {
	line := basic.CloneLine(g)
	if len(line) <= 4 || maths.DistOfLine(g) < tolerance {
		return line
	}

	pts := line.AsPts()
	pts = DouglasPeucker(pts, tolerance)
	if len(pts) == 0 {
		return nil
	}

	return basic.NewLineTruncatedFromPt(pts...)
}

func simplifyPolygon(g shigola.Polygon, tolerance float64) basic.Polygon {
	lines := g.Sublines()
	if len(lines) <= 0 {
		return nil
	}

	var poly basic.Polygon
	sqTolerance := tolerance * tolerance
	// First lets look the first line, then we will simplify the other lines.
	for i := range lines {
		area := maths.AreaOfPolygonLineString(lines[i])
		l := basic.CloneLine(lines[i])

		if area < sqTolerance {
			if i == 0 {
				return basic.ClonePolygon(g)
			}
			// don't simplify the internal line
			poly = append(poly, l)
			continue
		}

		pts := l.AsPts()
		if len(pts) <= 2 {
			if i == 0 {
				return nil
			}
			continue
		}

		pts = normalizePoints(pts)
		// If the last point is the same as the first, remove the first point.
		if len(pts) <= 4 {
			if i == 0 {
				return basic.ClonePolygon(g)
			}
			poly = append(poly, l)
			continue
		}

		pts = DouglasPeucker(pts, sqTolerance)
		if len(pts) <= 2 {
			if i == 0 {
				return nil
			}
			//log.Println("\t Skipping polygon subline.")
			continue
		}

		poly = append(poly, basic.NewLineTruncatedFromPt(pts...))
	}

	if len(poly) == 0 {
		return nil
	}

	return poly
}

func normalizePoints(pts []maths.Pt) (pnts []maths.Pt) {
	if pts[0] == pts[len(pts)-1] {
		pts = pts[1:]
	}

	if len(pts) <= 4 {
		return pts
	}

	lpt := 0
	pnts = append(pnts, pts[0])

	for i := 1; i < len(pts); i++ {
		ni := i + 1
		if ni >= len(pts) {
			ni = 0
		}
		m1, _, sdef1 := points.SlopeIntercept(pts[lpt], pts[i])
		m2, _, sdef2 := points.SlopeIntercept(pts[lpt], pts[ni])
		if m1 != m2 || sdef1 != sdef2 {
			pnts = append(pnts, pts[i])
		}
	}

	return pnts
}
