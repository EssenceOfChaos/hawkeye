import { Suspense } from 'react'
import { BrowserRouter, Navigate, Outlet, Route, Routes, useLocation } from 'react-router'
import { AuthProvider, useAuth } from './auth'
import { lazyWithReload } from './chunkReload'
import { ErrorBoundary } from './components/ErrorBoundary'
import { Layout } from './components/Layout'
import { ConfirmAlertAddressPage, ForgotPasswordPage, LoginPage, RegisterPage, ResetPasswordPage, VerifyEmailPage } from './pages/AuthPages'
import { AccountPage } from './pages/AccountPage'
import { ContactPage } from './pages/ContactPage'
import { BillingPage } from './pages/BillingPage'
import { IncidentsPage } from './pages/IncidentsPage'
import { LandingPage } from './pages/LandingPage'
import { MaintenancePage } from './pages/MaintenancePage'
import { NotificationsPage } from './pages/NotificationsPage'
import { MonitorFormPage } from './pages/MonitorFormPage'
import { StatusPage } from './pages/StatusPage'
import { StatusPageSettings } from './pages/StatusPageSettings'
import { PrivacyPage, TermsPage } from './pages/LegalPages'
import { MonitorsPage } from './pages/MonitorsPage'
import { NotFoundPage } from './pages/NotFoundPage'

// The chart pages pull in the charting library; load them only when visited so the landing and auth pages stay light.
const DashboardPage = lazyWithReload(() => import('./pages/DashboardPage').then((m) => ({ default: m.DashboardPage })))
const MonitorDetailPage = lazyWithReload(() => import('./pages/MonitorDetailPage').then((m) => ({ default: m.MonitorDetailPage })))

function RequireAuth() {
  const { status } = useAuth()
  if (status === 'loading') return <p role="status" className="center">Loading…</p>
  if (status === 'anonymous') return <Navigate to="/login" replace />
  return <Layout />
}

function Lazy({ children }: { children: React.ReactNode }) {
  return <Suspense fallback={<p role="status" className="center">Loading…</p>}>{children}</Suspense>
}

function PublicOnly() {
  const { status } = useAuth()
  if (status === 'loading') return <p role="status" className="center">Loading…</p>
  if (status === 'authenticated') return <Navigate to="/dashboard" replace />
  return (
    <main className="auth">
      <Outlet />
    </main>
  )
}

export function AppRoutes() {
  return (
    <Routes>
      <Route element={<PublicOnly />}>
        <Route path="/login" element={<LoginPage />} />
        <Route path="/register" element={<RegisterPage />} />
        <Route path="/forgot-password" element={<ForgotPasswordPage />} />
      </Route>
      <Route path="/" element={<LandingPage />} />
      <Route path="/contact" element={<ContactPage />} />
      <Route path="/privacy" element={<PrivacyPage />} />
      <Route path="/terms" element={<TermsPage />} />
      <Route path="/status/:slug" element={<StatusPage />} />
      <Route path="/reset-password" element={<main className="auth"><ResetPasswordPage /></main>} />
      <Route path="/confirm-alert-address" element={<main className="auth"><ConfirmAlertAddressPage /></main>} />
      <Route element={<RequireAuth />}>
        <Route path="/verify-email" element={<VerifyEmailPage />} />
        <Route path="/dashboard" element={<Lazy><DashboardPage /></Lazy>} />
        <Route path="/billing" element={<BillingPage />} />
        <Route path="/account" element={<AccountPage />} />
        <Route path="/incidents" element={<IncidentsPage />} />
        <Route path="/notifications" element={<NotificationsPage />} />
        <Route path="/maintenance" element={<MaintenancePage />} />
        <Route path="/status-page" element={<StatusPageSettings />} />
        <Route path="/monitors" element={<MonitorsPage />} />
        <Route path="/monitors/new" element={<MonitorFormPage />} />
        <Route path="/monitors/:monitorID" element={<Lazy><MonitorDetailPage /></Lazy>} />
        <Route path="/monitors/:monitorID/edit" element={<MonitorFormPage />} />
      </Route>
      <Route path="*" element={<NotFoundPage />} />
    </Routes>
  )
}

/** Routes inside an error boundary that resets when the page changes. */
function GuardedRoutes() {
  const { pathname } = useLocation()
  return (
    <ErrorBoundary resetKey={pathname}>
      <AppRoutes />
    </ErrorBoundary>
  )
}

export default function App() {
  return (
    <BrowserRouter>
      <AuthProvider>
        <GuardedRoutes />
      </AuthProvider>
    </BrowserRouter>
  )
}
