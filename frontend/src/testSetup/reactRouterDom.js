// jest moduleNameMapper shim for the `react-router/dom` subpath (CRA 5's
// jest does not resolve package.json "exports" maps).
require('./globals');
module.exports = require('react-router/dist/development/dom-export.js');
