import React from "react";
import ReactDOM from "react-dom/client";
import { createBrowserRouter, RouterProvider } from "react-router-dom";
import App from "./App";
import Overview from "./routes/Overview";
import Flows from "./routes/Flows";
import FlowGraph from "./routes/FlowGraph";
import Tasks from "./routes/Tasks";
import TaskDetail from "./routes/TaskDetail";
import Providers from "./routes/Providers";
import ProviderDetail from "./routes/ProviderDetail";
import Endpoints from "./routes/Endpoints";
import Logic from "./routes/Logic";
import Diagnostics from "./routes/Diagnostics";
import EditorPage from "./routes/Editor";
import Git from "./routes/Git";
import Users from "./routes/Users";
import Playground from "./routes/Playground";
import Login from "./routes/Login";
import { AuthGate } from "./components/AuthGate";
import "./styles.css";

// /login is public; everything else is wrapped by AuthGate which checks
// /api/me and redirects to /login on 401. Local mode always reports
// authenticated, so the gate is transparent in that mode.
const router = createBrowserRouter([
  { path: "/login", Component: Login },
  {
    path: "/",
    element: <AuthGate>{(user) => <App user={user} />}</AuthGate>,
    children: [
      { index: true, Component: Overview },
      { path: "flows", Component: Flows },
      { path: "flows/:name", Component: FlowGraph },
      { path: "tasks", Component: Tasks },
      { path: "tasks/:name", Component: TaskDetail },
      { path: "providers", Component: Providers },
      { path: "providers/:name", Component: ProviderDetail },
      { path: "endpoints", Component: Endpoints },
      { path: "logic", Component: Logic },
      { path: "diagnostics", Component: Diagnostics },
      { path: "editor", Component: EditorPage },
      { path: "git", Component: Git },
      { path: "users", Component: Users },
      { path: "playground", Component: Playground },
    ],
  },
]);

ReactDOM.createRoot(document.getElementById("root")!).render(
  <React.StrictMode>
    <RouterProvider router={router} />
  </React.StrictMode>,
);
