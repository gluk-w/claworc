import "@testing-library/jest-dom/vitest";

// jsdom does not implement scrollIntoView
Element.prototype.scrollIntoView = () => {};
