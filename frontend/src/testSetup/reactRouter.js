// jest moduleNameMapper shim for `react-router`.
//
// CRA 5's jest (jest 27) resolves the bare specifier through the package's
// "main" field but not its "exports" map, and the CJS bundle needs the
// TextEncoder polyfill before it is loaded.
require('./globals');
module.exports = require('react-router/dist/development/index.js');
