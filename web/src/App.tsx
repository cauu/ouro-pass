import { createBrowserRouter, Navigate, RouterProvider } from "react-router-dom";
import { RequireAuth, RequireRole } from "./app/guards";
import { Layout } from "./app/Layout";
import { Placeholder } from "./app/Placeholder";
import { LoginPage } from "./features/auth/LoginPage";

const router = createBrowserRouter([
  { path: "/login", element: <LoginPage /> },
  {
    path: "/",
    element: (
      <RequireAuth>
        <Layout />
      </RequireAuth>
    ),
    children: [
      { index: true, element: <Navigate to="/dashboard" replace /> },
      { path: "dashboard", element: <Placeholder name="Dashboard" /> },
      { path: "members", element: <Placeholder name="Members" /> },
      { path: "subscriptions", element: <Placeholder name="Subscriptions" /> },
      {
        path: "rules",
        element: (
          <RequireRole min="operator">
            <Placeholder name="Rules" />
          </RequireRole>
        ),
      },
      {
        path: "channels",
        element: (
          <RequireRole min="operator">
            <Placeholder name="Channels" />
          </RequireRole>
        ),
      },
      {
        path: "push",
        element: (
          <RequireRole min="operator">
            <Placeholder name="Push" />
          </RequireRole>
        ),
      },
      {
        path: "clients",
        element: (
          <RequireRole min="owner">
            <Placeholder name="OAuth Clients" />
          </RequireRole>
        ),
      },
      {
        path: "keys",
        element: (
          <RequireRole min="owner">
            <Placeholder name="Signing Keys" />
          </RequireRole>
        ),
      },
      {
        path: "audit",
        element: (
          <RequireRole min="owner">
            <Placeholder name="Audit" />
          </RequireRole>
        ),
      },
      {
        path: "setup",
        element: (
          <RequireRole min="owner">
            <Placeholder name="Setup" />
          </RequireRole>
        ),
      },
      { path: "*", element: <Navigate to="/dashboard" replace /> },
    ],
  },
]);

export default function App() {
  return <RouterProvider router={router} />;
}
