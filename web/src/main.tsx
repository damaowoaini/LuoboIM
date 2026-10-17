import "@ant-design/v5-patch-for-react-19";
import "./index.scss";
import "./i18n/index";

import log from "electron-log/renderer";
import ReactDOM from "react-dom/client";

import App from "./App";
import { isSaveLog } from "./config";
import { initializeDevEndpoints } from "./core/session/devEndpoints";
import { initializeDevObjectTransport } from "./core/session/devObjectTransport";

if (window.electronAPI && isSaveLog) {
  const sdkLogger = log.scope("openim-sdk-core");
  console.debug = sdkLogger.debug.bind(sdkLogger);
  const rendererLogger = log.scope("renderer");
  console.log = rendererLogger.log.bind(rendererLogger);
  console.error = rendererLogger.error.bind(rendererLogger);
}

void initializeDevEndpoints().then(initializeDevObjectTransport).then(() => {
  ReactDOM.createRoot(document.getElementById("root") as HTMLElement).render(<App />);
  postMessage({ payload: "removeLoading" }, "*");
});
