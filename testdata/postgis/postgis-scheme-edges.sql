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
-- The geometry is 4326 because both schemes' tiles can be expressed in it, and
-- because one of these points is above the highest latitude a 3857 column could
-- hold.
--
-- The points sat where a 4326 layer was exact in both schemes -- the equator, a
-- tile edge, or the scheme that reaches them natively -- because ST_AsMVTGeom
-- was handed !BBOX! in the layer's own SRID, which for WebMercatorQuad made the
-- mapping linear in latitude where the grid is linear in mercator y. That was a
-- workaround for MAPCO-11614, not a property of tiling: the layer SQL clips
-- against !TILE_BBOX! now, so a general latitude is exact in either scheme.
--
-- They stay where they are anyway. Where the two schemes differ in shape rather
-- than in accuracy -- the poles, the antimeridian, a tile corner, the shallowest
-- zoom -- is still what this fixture is for, and postgis-cross-crs.sql covers
-- the general latitudes.

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
    (4, 'corner',       ST_SetSRID(ST_MakePoint(0,            0),        4326)),

    -- A general mid-latitude, on neither the equator nor a tile edge. Every
    -- other point here is at a latitude where spacing a tile by latitude and
    -- spacing it by mercator y give the same answer, which is what made this
    -- fixture blind to MAPCO-11614: in WebMercatorQuad it belongs at y=1473 and
    -- the defect put it at 964, a difference of an eighth of the tile that no
    -- point in this table could express.
    --
    -- Exact in both schemes: 45 degrees is a quarter of WorldCRS84Quad zoom 0's
    -- 180-degree height, so y=1024 there, and the mercator value is pinned by
    -- the goldens.
    (5, 'midlat',       ST_SetSRID(ST_MakePoint(-45,          45),       4326));

CREATE INDEX scheme_edges_geom_idx ON scheme_edges USING GIST (geom);
