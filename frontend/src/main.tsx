// SPDX-License-Identifier: FSL-1.1-Apache-2.0
import React from "react";
import ReactDOM from "react-dom/client";
import { BrowserRouter } from "react-router-dom";
import App from "./App";
// Fonts are bundled rather than loaded from a font service, so opening the
// app sends no request to a third party (Privacy Policy: no data outside
// the EU/EEA, only strictly necessary cookies). The weights are the ones
// the styles use.
import "@fontsource/inter/400.css";
import "@fontsource/inter/500.css";
import "@fontsource/inter/600.css";
import "@fontsource/inter/700.css";
import "@fontsource/jetbrains-mono/400.css";
import "@fontsource/jetbrains-mono/500.css";
import "./styles.css";

ReactDOM.createRoot(document.getElementById("root")!).render(
  <React.StrictMode>
    <BrowserRouter>
      <App />
    </BrowserRouter>
  </React.StrictMode>
);
