package basic

import "github.com/MapColonies/shigola"

// ClonePoint will return a basic.Point for given shigola.Point.
func ClonePoint(pt shigola.Point) Point {
	return Point{pt.X(), pt.Y()}
}

// ClonePoint will return a basic.Point3 for given shigola.Point3.
func ClonePoint3(pt shigola.Point3) Point3 {
	return Point3{pt.X(), pt.Y(), pt.Z()}
}

// CloneMultiPoint will return a basic.MultiPoint for the given tegol.MultiPoint
func CloneMultiPoint(mpt shigola.MultiPoint) MultiPoint {
	var bmpt MultiPoint
	for _, pt := range mpt.Points() {
		bmpt = append(bmpt, ClonePoint(pt))
	}
	return bmpt
}

/*
// CloneMultiPoint3 will return a basic.MultiPoint3 for the given tegol.MultiPoint3
func CloneMultiPoint3(mpt shigola.MultiPoint3) MultiPoint3 {
	var bmpt MultiPoint3
	for _, pt := range mpt.Points() {
		bmpt = append(bmpt, ClonePoint3(pt))
	}
	return bmpt
}
*/

// CloneLine will return a basic.Line for a given shigola.LineString
func CloneLine(line shigola.LineString) (l Line) {
	for _, pt := range line.Subpoints() {
		l = append(l, Point{pt.X(), pt.Y()})
	}
	return l
}

// CloneMultiLine will return a basic.MultiLine for a given togola.MultiLine
func CloneMultiLine(mline shigola.MultiLine) (ml MultiLine) {
	for _, ln := range mline.Lines() {
		ml = append(ml, CloneLine(ln))
	}
	return ml
}

// ClonePolygon will return a basic.Polygon for a given shigola.Polygon
func ClonePolygon(polygon shigola.Polygon) (ply Polygon) {
	for _, ln := range polygon.Sublines() {
		ply = append(ply, CloneLine(ln))
	}
	return ply
}

// CloneMultiPolygon will return a basic.MultiPolygon for a given shigola.MultiPolygon.
func CloneMultiPolygon(mpolygon shigola.MultiPolygon) (mply MultiPolygon) {
	for _, ply := range mpolygon.Polygons() {
		mply = append(mply, ClonePolygon(ply))
	}
	return mply
}

func Clone(geo shigola.Geometry) Geometry {
	switch g := geo.(type) {
	case shigola.Point:
		return ClonePoint(g)
	case shigola.MultiPoint:
		return CloneMultiPoint(g)
	case shigola.LineString:
		return CloneLine(g)
	case shigola.MultiLine:
		return CloneMultiLine(g)
	case shigola.Polygon:
		return ClonePolygon(g)
	case shigola.MultiPolygon:
		return CloneMultiPolygon(g)
	}
	return nil
}
