MIT License

Copyright (c) 2016 The Tegola Authors (https://github.com/go-spatial/tegola)
Copyright (c) 2020 Development Seed (https://github.com/developmentseed/morecantile)
Copyright (c) 2026 MapColonies

Permission is hereby granted, free of charge, to any person obtaining a copy
of this software and associated documentation files (the "Software"), to deal
in the Software without restriction, including without limitation the rights
to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
copies of the Software, and to permit persons to whom the Software is
furnished to do so, subject to the following conditions:

The above copyright notice and this permission notice shall be included in all
copies or substantial portions of the Software.

THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
SOFTWARE.


================================================================================
ATTRIBUTION
================================================================================

This software incorporates work from the projects below. Each is MIT licensed,
and each copyright notice above corresponds to one of them. The notices are
reproduced here so they travel with this file.

--------------------------------------------------------------------------------
Tegola — https://github.com/go-spatial/tegola
--------------------------------------------------------------------------------

Copyright (c) 2016 The Tegola Authors

Shigola is a FORK of Tegola, an open source vector tile server created and
maintained by the Go Spatial team and documented at https://tegola.io.
Effectively all of this codebase originates there: the data providers, the
geometry processing, the MVT encoder, the tile pipeline and the cache backends
are all Tegola's work. Shigola renames the project, adds an OGC API - Tiles
surface, a TileMatrixSet registry and a layered cache, and makes no commitment
to remain compatible with Tegola.

Tegola is released under the MIT License, the full text of which appears above
and applies to those portions. Its contributors are credited in CONTRIBUTORS.md
and its release history is retained verbatim in CHANGELOG.md.

--------------------------------------------------------------------------------
morecantile — https://github.com/developmentseed/morecantile
--------------------------------------------------------------------------------

Copyright (c) 2020 Development Seed

The tms/ package is a faithful Go port of morecantile 7.0.3, not merely a
reimplementation inspired by it. Its OGC "Two Dimensional Tile Matrix Set"
document model, its tile algorithms, the thirteen bundled grid definitions under
tms/data/, and its test suite were all translated from the Python original, and
morecantile's golden values serve as the port's correctness oracle.

morecantile is released under the MIT License. Its licence text is reproduced in
full, unmodified, at tms/LICENSE-morecantile.

--------------------------------------------------------------------------------

Vendored Go dependencies under vendor/ retain their own licence files within
their module directories. See NOTICE.md for the complete list of third-party
works redistributed with this software.
