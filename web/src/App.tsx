import { createBrowserRouter, Navigate, RouterProvider } from "react-router-dom";
import { RequireAuth, RequireRole } from "./app/guards";
import { Layout } from "./app/Layout";
import { AuditPage } from "./features/audit/AuditPage";
import { LoginPage } from "./features/auth/LoginPage";
import { ChannelsPage } from "./features/channels/ChannelsPage";
import { ClientsPage } from "./features/clients/ClientsPage";
import { DashboardPage } from "./features/dashboard/DashboardPage";
import { KeysPage } from "./features/keys/KeysPage";
import { MembersPage } from "./features/members/MembersPage";
import { PushPage } from "./features/push/PushPage";
import { RulesPage } from "./features/rules/RulesPage";
import { SetupPage } from "./features/setup/SetupPage";
import { SubscriptionsPage } from "./features/subscriptions/SubscriptionsPage";

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
      { path: "dashboard", element: <DashboardPage /> },
      { path: "members", element: <MembersPage /> },
      { path: "subscriptions", element: <SubscriptionsPage /> },
      {
        path: "rules",
        element: (
          <RequireRole min="operator">
            <RulesPage />
          </RequireRole>
        ),
      },
      {
        path: "channels",
        element: (
          <RequireRole min="operator">
            <ChannelsPage />
          </RequireRole>
        ),
      },
      {
        path: "push",
        element: (
          <RequireRole min="operator">
            <PushPage />
          </RequireRole>
        ),
      },
      {
        path: "clients",
        element: (
          <RequireRole min="owner">
            <ClientsPage />
          </RequireRole>
        ),
      },
      {
        path: "keys",
        element: (
          <RequireRole min="owner">
            <KeysPage />
          </RequireRole>
        ),
      },
      {
        path: "audit",
        element: (
          <RequireRole min="owner">
            <AuditPage />
          </RequireRole>
        ),
      },
      {
        path: "setup",
        element: (
          <RequireRole min="owner">
            <SetupPage />
          </RequireRole>
        ),
      },
      { path: "*", element: <Navigate to="/dashboard" replace /> },
    ],
  },
], { basename: "/admin" });

export default function App() {
  return <RouterProvider router={router} />;
}
