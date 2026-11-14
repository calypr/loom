import base from './graph-rows.playwright.config.mjs';
// Reuse the official native fixture launcher, functional bounds and zero retries.
export default {...base,testMatch:'traversal-first-comparison.spec.mjs'};
