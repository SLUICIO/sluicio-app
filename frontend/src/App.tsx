// SPDX-License-Identifier: FSL-1.1-Apache-2.0
import type { ReactElement } from "react";
import { Navigate, Route, Routes } from "react-router-dom";
import AppShell from "./components/AppShell";
import UserProvider from "./components/UserProvider";
import { useCurrentUser } from "./lib/useCurrentUser";
import Alerts from "./pages/Alerts";
import Health from "./pages/Health";
import IntegrationDetail from "./pages/IntegrationDetail";
import IntegrationErrors from "./pages/IntegrationErrors";
import IntegrationMessages from "./pages/IntegrationMessages";
import IntegrationNew from "./pages/IntegrationNew";
import IntegrationLogs from "./pages/IntegrationLogs";
import IntegrationMetadata from "./pages/IntegrationMetadata";
import IntegrationServices from "./pages/IntegrationServices";
import IntegrationSettings from "./pages/IntegrationSettings";
import Integrations from "./pages/Integrations";
import LogsPage from "./pages/LogsPage";
import MapsPage from "./pages/MapsPage";
import MetadataFieldsPage from "./pages/MetadataFieldsPage";
import MetricsPage from "./pages/MetricsPage";
import SchemasPage from "./pages/SchemasPage";
import Search from "./pages/Search";
import Account from "./pages/Account";
import Operator from "./pages/Operator";
import Settings from "./pages/Settings";
import ServiceDetail from "./pages/ServiceDetail";
import Services from "./pages/Services";
import Systems from "./pages/Systems";
import SystemDetail from "./pages/SystemDetail";
import MonitoringTemplates from "./pages/MonitoringTemplates";
import SystemTypesPage from "./pages/SystemTypesPage";
import Developers from "./pages/Developers";
import ServiceTypeDetail from "./pages/ServiceTypeDetail";
import ServiceTypes from "./pages/ServiceTypes";
import StuckMessages from "./pages/StuckMessages";
import Tags from "./pages/Tags";
import Topology from "./pages/Topology";
import TraceDetail from "./pages/TraceDetail";
import Usage from "./pages/Usage";

// Routes behind org-admin. Viewers/operators/contributors lack
// "org.manage", so they're bounced to /health instead of seeing Settings.
function RequireOrgAdmin({ children }: { children: ReactElement }) {
  const { can } = useCurrentUser();
  return can("org.manage") ? children : <Navigate to="/health" replace />;
}

// Routes behind the cell-operator flag (super-admin above the org roles).
// Non-operators are bounced to /health.
function RequireOperator({ children }: { children: ReactElement }) {
  const { isOperator } = useCurrentUser();
  return isOperator ? children : <Navigate to="/health" replace />;
}

export default function App() {
  // UserProvider gates everything below — on a 401 from /api/v1/me
  // it renders the Login page instead of these routes.
  return (
    <UserProvider>
      <Routes>
        <Route element={<AppShell />}>
        <Route index element={<Navigate to="/health" replace />} />
        <Route path="/health" element={<Health />} />
        <Route path="/services" element={<Services />} />
        <Route path="/systems" element={<Systems />} />
        <Route path="/systems/:id" element={<SystemDetail />} />
        <Route path="/services/:name" element={<ServiceDetail />} />
        <Route path="/integrations" element={<Integrations />} />
        <Route path="/integrations/new" element={<IntegrationNew />} />
        <Route path="/integrations/:id" element={<IntegrationDetail />} />
        <Route
          path="/integrations/:id/messages"
          element={<IntegrationMessages />}
        />
        <Route path="/integrations/:id/logs" element={<IntegrationLogs />} />
        <Route path="/integrations/:id/services" element={<IntegrationServices />} />
        <Route path="/integrations/:id/metadata" element={<IntegrationMetadata />} />
        <Route path="/integrations/:id/errors" element={<IntegrationErrors />} />
        <Route path="/integrations/:id/settings" element={<IntegrationSettings />} />
        <Route path="/monitoring-templates" element={<MonitoringTemplates />} />
        <Route path="/system-types" element={<SystemTypesPage />} />
        <Route path="/developers" element={<Developers />} />
        <Route path="/service-facets" element={<ServiceTypes />} />
        <Route path="/service-facets/:slug" element={<ServiceTypeDetail />} />
        <Route path="/tags" element={<Tags />} />
        <Route path="/metadata-fields" element={<MetadataFieldsPage />} />
        <Route path="/schemas" element={<SchemasPage />} />
        <Route path="/maps" element={<MapsPage />} />
        <Route
          path="/usage"
          element={
            <RequireOrgAdmin>
              <Usage />
            </RequireOrgAdmin>
          }
        />
        <Route path="/traces/:traceId" element={<TraceDetail />} />
        <Route path="/search" element={<Search />} />
        <Route path="/logs" element={<LogsPage />} />
        <Route path="/metrics" element={<MetricsPage />} />
        <Route path="/topology" element={<Topology />} />
        <Route path="/stuck" element={<StuckMessages />} />
        <Route path="/alerts" element={<Alerts />} />
        <Route
          path="/settings"
          element={
            <RequireOrgAdmin>
              <Settings />
            </RequireOrgAdmin>
          }
        />
        <Route path="/account" element={<Account />} />
        <Route
          path="/operator"
          element={
            <RequireOperator>
              <Operator />
            </RequireOperator>
          }
        />
        </Route>
      </Routes>
    </UserProvider>
  );
}
