-- Fixture for the tile-content checks (MAPCO-11547, and the tickets built on it).
--
-- Three layers, eight features. Every coordinate was chosen backwards: from the
-- tile-space integer it should produce, to the longitude and latitude that
-- produce it. That is what makes the expected values in the tests arithmetic a
-- reviewer can check rather than numbers a generator emitted.
--
-- KEEP THIS SMALL. The golden files that pin these tiles are honest only while
-- they are short enough to read in a pull-request diff. The Athens fixture next
-- door holds 633 features on one conformance tile, about 4300 coordinate pairs;
-- a golden that size gets regenerated and blessed rather than reviewed, and the
-- check then passes while asserting nothing.
--
-- The requested tile is WorldCRS84Quad 3/2/10 -- zoom 3, row 2, column 10 --
-- spanning longitude 45..67.5 and latitude 22.5..45. That scheme is linear in
-- longitude and latitude, so a 4326 layer is exact in it everywhere, and these
-- points can sit wherever the arithmetic wants them. WebMercatorQuad is not:
-- see testdata/postgis/postgis-scheme-edges.sql and .github/cite/config.toml.
--
-- The column is deliberately larger than the row count. WorldCRS84Quad is twice
-- as wide as it is tall, so at zoom 3 there are 16 columns and 8 rows: column 10
-- exists and row 10 does not. A handler reading the two path segments the wrong
-- way round therefore gets a rejection rather than a plausible-looking tile,
-- and MAPCO-11550 turns that into an explicit check.
--
-- ST_AsMVTGeom's buffer defaults to 256, not to 0. The epic said otherwise, and
-- the crossing road below is what showed it: geometry is clipped at the extent
-- plus that buffer, so a feature meant to be excluded has to sit more than 256
-- tile units -- here 1.40625 degrees -- outside the tile.

DROP TABLE IF EXISTS tile_content_places;
DROP TABLE IF EXISTS tile_content_roads;
DROP TABLE IF EXISTS tile_content_far;

-- One column of each MVT value type, with the values distinct between features,
-- so a feature carrying its neighbour's row fails rather than passing by
-- coincidence. note is null on exactly one feature: ST_AsMVT omits a
-- null-valued tag from that feature while the key stays in the layer's
-- dictionary, because other features still carry it.
CREATE TABLE tile_content_places (
    fid    integer PRIMARY KEY,
    name   text NOT NULL,
    rank   integer NOT NULL,
    score  double precision NOT NULL,
    active boolean NOT NULL,
    note   text,
    geom   geometry(Point, 4326) NOT NULL
);

INSERT INTO tile_content_places (fid, name, rank, score, active, note, geom) VALUES
    -- The tile's centre: exactly half the extent in both axes.
    (1, 'centre',  10, 1.5,  true,  'centre note',
        ST_SetSRID(ST_MakePoint( 56.25,          33.75),         4326)),

    -- The zoom-pyramid probe (MAPCO-11551): the centre of a WorldCRS84Quad
    -- zoom-11 tile, which lands on whole tile-space coordinates at every
    -- shallower zoom and on a tile boundary at none of them. At this tile's
    -- zoom it is (2248,1416).
    (2, 'probe',   20, 2.25, false, 'probe note',
        ST_SetSRID(ST_MakePoint( 57.3486328125,  37.2216796875), 4326)),

    -- note is null here, and only here.
    (3, 'nulltag', 30, 3.75, true,  NULL,
        ST_SetSRID(ST_MakePoint( 61.875,         39.375),        4326)),

    -- East of the requested tile, and far enough east to clear the 256-unit
    -- buffer, so "no more" has something to fail against.
    (4, 'outside', 40, 4.5,  false, 'outside note',
        ST_SetSRID(ST_MakePoint( 70.0,           33.75),         4326));

CREATE INDEX tile_content_places_geom_idx ON tile_content_places USING GIST (geom);

-- Lines, so clipping at the tile edge is assertable.
CREATE TABLE tile_content_roads (
    fid   integer PRIMARY KEY,
    name  text NOT NULL,
    lanes integer NOT NULL,
    geom  geometry(LineString, 4326) NOT NULL
);

INSERT INTO tile_content_roads (fid, name, lanes, geom) VALUES
    -- Runs off the tile's eastern edge, so it arrives cut at the extent plus
    -- ST_AsMVTGeom's default 256 buffer: (3072,2048) to (4352,2048).
    (1, 'crossing', 2,
        ST_SetSRID(ST_MakeLine(ST_MakePoint(61.875, 33.75), ST_MakePoint(72.0, 33.75)), 4326)),

    -- Wholly inside: (1024,1024) to (2048,1024).
    (2, 'inside',   4,
        ST_SetSRID(ST_MakeLine(ST_MakePoint(50.625, 39.375), ST_MakePoint(56.25, 39.375)), 4326));

CREATE INDEX tile_content_roads_geom_idx ON tile_content_roads USING GIST (geom);

-- Every row is outside the requested tile, so a layer that should not appear at
-- all has something to fail against.
CREATE TABLE tile_content_far (
    fid  integer PRIMARY KEY,
    name text NOT NULL,
    geom geometry(Point, 4326) NOT NULL
);

INSERT INTO tile_content_far (fid, name, geom) VALUES
    (1, 'far a', ST_SetSRID(ST_MakePoint(-100.0,  0.0), 4326)),
    (2, 'far b', ST_SetSRID(ST_MakePoint(-120.0, 10.0), 4326));

CREATE INDEX tile_content_far_geom_idx ON tile_content_far USING GIST (geom);
