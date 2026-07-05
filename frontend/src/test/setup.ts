// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// Vitest global setup. Registers jest-dom's custom matchers (e.g.
// toBeInTheDocument, toHaveTextContent) and their type augmentation, and
// unmounts React trees between tests so the jsdom document stays clean.
import "@testing-library/jest-dom/vitest";
import { afterEach } from "vitest";
import { cleanup } from "@testing-library/react";

afterEach(() => {
  cleanup();
});
