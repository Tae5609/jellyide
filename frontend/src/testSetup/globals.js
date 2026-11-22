// Jest globals polyfill (used by the react-router shims below).
//
// react-router v7's CJS bundle uses TextEncoder/TextDecoder, which the
// jsdom environment of CRA's jest (jsdom 16) does not provide.
const { TextEncoder, TextDecoder } = require('util');

if (typeof globalThis.TextEncoder === 'undefined') {
  globalThis.TextEncoder = TextEncoder;
}
if (typeof globalThis.TextDecoder === 'undefined') {
  globalThis.TextDecoder = TextDecoder;
}
