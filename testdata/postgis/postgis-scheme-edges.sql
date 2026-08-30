-- Fixture for the tile-content checks (MAPCO-11545).
--
-- Four points, placed where the two tiling schemes stop agreeing. Every
-- coordinate here was chosen backwards: from the tile-space integer the point
-- should produce, to the longitude and latitude that produce it. That is what
-- makes the expected values in the tests arithmetic a reviewer can check rather
-- than numbers a generator emitted.
--
-- KEEP THIS TABLE TINY. The golden files that pin these tiles are only honest
-- while they are small enough to read in a pull-request diff. The Athens
-- fixture next door holds 633 features on one conformance tile, about 4300
-- coordinate pairs; a golden that size gets regenerated and blessed rather than
-- reviewed, and the check then passes while asserting nothing.
--
-- The geometry is 4326 because both schemes' tiles can be expressed in it. The
-- points sit where a 4326 layer is exact in both schemes: on the equator, on a
-- tile edge, or in the scheme that reaches them natively. ST_AsMVTGeom maps the
-- bounding box onto the tile grid affinely, and for WebMercatorQuad that is
-- linear in latitude where the true grid is linear in mercator y -- an error
-- that is zero at a tile's own edges and at the equator, and largest in
-- between. See .github/cite/config.toml for the arithmetic. Anything placed at
-- a general latitude would need a per-scheme layer to be exact.

DROP TABLE IF EXISTS scheme_edges;

CREATE TABLE scheme_edges (
    fid  integer PRIMARY KEY,
    name text NOT NULL,
    geom geometry(Point, 4326) NOT NULL
);

INSERT INTO scheme_edges (fid, name, geom) VALUES
    -- Interior of both schemes' shallowest tile, on the equator, a quarter of
    -- the world west of the prime meridian. The ordinary case the edges are
    -- measured against.
    (1, 'origin',       ST_SetSRID(ST_MakePoint(-90,          0),        4326)),

    -- Above the highest latitude WebMercatorQuad can express (85.0511287798).
    -- WorldCRS84Quad reaches the pole and serves it; WebMercatorQuad has no
    -- tile covering this ground at any zoom, which is a fact about the scheme
    -- rather than an error.
    (2, 'polar',        ST_SetSRID(ST_MakePoint(-90,          87.1875),  4326)),

    -- Eight units short of the antimeridian in WebMercatorQuad's shallowest
    -- tile, sixteen in WorldCRS84Quad's last column. Where an off-by-one in the
    -- column arithmetic, or a longitude wrapped the wrong way, produces a tile
    -- that looks plausible and holds the wrong ground.
    (3, 'antimeridian', ST_SetSRID(ST_MakePoint(179.296875,   0),        4326)),

    -- Exactly on a tile corner at zoom 1 in both schemes: the prime meridian
    -- meets the equator, where four tiles meet. Selection is by bounding-box
    -- intersection, which includes the boundary, so this point belongs to all
    -- four. That is deliberate -- see the tests that pin it.
    (4, 'corner',       ST_SetSRID(ST_MakePoint(0,            0),        4326));

CREATE INDEX scheme_edges_geom_idx ON scheme_edges USING GIST (geom);
