import { BrowserRouter, Navigate, Outlet, Route, Routes } from 'react-router'
import { AuthProvider, useAuth } from './auth'
import { Layout } from './components/Layout'
import { ConfirmAlertAddressPage, ForgotPasswordPage, LoginPage, RegisterPage, ResetPasswordPage, VerifyEmailPage } from './pages/AuthPages'
import { IncidentsPage } from './pages/IncidentsPage'
import { LandingPage } from './pages/LandingPage'
import { NotificationsPage } from './pages/NotificationsPage'
import { MonitorFormPage } from './pages/MonitorFormPage'
import { MonitorsPage } from './pages/MonitorsPage'

function RequireAuth() {
  const { status } = useAuth()
  if (status === 'loading') return <p role="status" className="center">Loading…</p>
  if (status === 'anonymous') return <Navigate to="/login" replace />
  return <Layout />
}

function PublicOnly() {
  const { status } = useAuth()
  if (status === 'loading') return <p role="status" className="center">Loading…</p>
  if (status === 'authenticated') return <Navigate to="/monitors" replace />
  return (
    <main>
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
      <Route path="/reset-password" element={<main><ResetPasswordPage /></main>} />
      <Route path="/confirm-alert-address" element={<main><ConfirmAlertAddressPage /></main>} />
      <Route element={<RequireAuth />}>
        <Route path="/verify-email" element={<VerifyEmailPage />} />
        <Route path="/incidents" element={<IncidentsPage />} />
        <Route path="/notifications" element={<NotificationsPage />} />
        <Route path="/monitors" element={<MonitorsPage />} />
        <Route path="/monitors/new" element={<MonitorFormPage />} />
        <Route path="/monitors/:monitorID/edit" element={<MonitorFormPage />} />
        <Route path="*" element={<Navigate to="/monitors" replace />} />
      </Route>
    </Routes>
  )
}

export default function App() {
  return (
    <BrowserRouter>
      <AuthProvider>
        <AppRoutes />
      </AuthProvider>
    </BrowserRouter>
  )
}
