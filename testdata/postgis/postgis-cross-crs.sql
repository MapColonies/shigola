-- Fixture for the cross-CRS tile-content checks (MAPCO-11599).
--
-- Three points on one meridian, served twice over: once from the 4326 column
-- below, and once from the same column transformed to 3857 by the layer SQL.
-- The tile a scheme produces must not depend on which of the two it was, and
-- that is the whole point of the table -- so it stays three rows, and the
-- interesting part lives in the test's layer definitions rather than here.
--
-- The latitudes were chosen backwards from the tile-space integers they should
-- produce. WorldCRS84Quad row 0 at zoom 0 spans 90..-90 over 4096 units, so
-- y = (90 - lat) / 180 * 4096: lat 45 is 1024, lat 78.75 is 256, the equator is
-- 2048. All three are exact, which is what makes the expected values in the
-- test arithmetic a reviewer can check.
--
-- Every point sits below mercator's top latitude, unlike scheme_edges next
-- door. A 3857 column cannot hold anything above it, and this table's job is to
-- be the same ground in both SRIDs.

DROP TABLE IF EXISTS cross_crs;

CREATE TABLE cross_crs (
    fid  integer PRIMARY KEY,
    name text NOT NULL,
    geom geometry(Point, 4326) NOT NULL
);

INSERT INTO cross_crs (fid, name, geom) VALUES
    -- The equator, where every projection of latitude agrees and every version
    -- of this code produced the same answer. The control.
    (1, 'equator', ST_SetSRID(ST_MakePoint(-90,  0),     4326)),

    -- A general mid-latitude: far enough from the equator and from any tile
    -- edge that spacing the tile by the wrong CRS's axis is unmissable.
    (2, 'mid',     ST_SetSRID(ST_MakePoint(-90, 45),     4326)),

    -- High enough that the two projections have diverged by most of the tile,
    -- and still inside mercator's range.
    (3, 'high',    ST_SetSRID(ST_MakePoint(-90, 78.75),  4326));

CREATE INDEX cross_crs_geom_idx ON cross_crs USING GIST (geom);
