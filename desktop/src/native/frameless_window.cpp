#include "frameless_window.h"

#include <QGuiApplication>
#include <QWindow>

#ifdef Q_OS_WIN
#  include <windows.h>
#  include <windowsx.h>
#  include <dwmapi.h>
#endif

namespace
{
#ifdef Q_OS_WIN
constexpr int kResizeBorderDip = 5;
constexpr DWORD kDwmwaCornerPreference = 33;
constexpr DWORD kDwmwaBorderColor = 34;

RECT workArea(HWND hwnd)
{
    MONITORINFO info{};
    info.cbSize = sizeof(info);
    ::GetMonitorInfoW(::MonitorFromWindow(hwnd, MONITOR_DEFAULTTONEAREST), &info);
    return info.rcWork;
}
#endif
} // namespace

FramelessWindow::FramelessWindow(QWindow *window)
    : m_window(window)
{
#ifdef Q_OS_WIN
    if (!m_window)
        return;

    const auto hwnd = reinterpret_cast<HWND>(m_window->winId());

    // Keep the resize frame and the system menu, drop the caption. Without
    // WS_THICKFRAME the window cannot be resized by the mouse at all, and
    // DWM would not give it a shadow.
    auto style = ::GetWindowLongPtrW(hwnd, GWL_STYLE);
    // No WS_CAPTION: keeping it would make Windows paint a caption strip that
    // WM_NCCALCSIZE then has to erase, which flashes white on every resize.
    style |= WS_THICKFRAME | WS_SYSMENU | WS_MAXIMIZEBOX | WS_MINIMIZEBOX;
    style &= ~WS_CAPTION;
    ::SetWindowLongPtrW(hwnd, GWL_STYLE, style);
    ::SetWindowPos(hwnd, nullptr, 0, 0, 0, 0,
                   SWP_FRAMECHANGED | SWP_NOMOVE | SWP_NOSIZE | SWP_NOZORDER | SWP_NOACTIVATE);

    // Win11 rounds every window by default; this product has radius 0.
    DWORD corner = 1; // DWMWCP_DONOTROUND
    ::DwmSetWindowAttribute(hwnd, kDwmwaCornerPreference, &corner, sizeof(corner));

    // A hairline that matches the ink instead of the system accent.
    const COLORREF border = RGB(0x2A, 0x2A, 0x2A);
    ::DwmSetWindowAttribute(hwnd, kDwmwaBorderColor, &border, sizeof(border));

    if (auto *app = QGuiApplication::instance()) {
        app->installNativeEventFilter(this);
        m_installed = true;
    }
#endif
}

FramelessWindow::~FramelessWindow()
{
#ifdef Q_OS_WIN
    if (m_installed) {
        if (auto *app = QGuiApplication::instance())
            app->removeNativeEventFilter(this);
    }
#endif
}

bool FramelessWindow::nativeEventFilter(const QByteArray &eventType, void *message, qintptr *result)
{
#ifdef Q_OS_WIN
    if (eventType != "windows_generic_MSG")
        return false;

    auto *msg = static_cast<MSG *>(message);
    if (!m_window || !msg)
        return false;
    if (msg->hwnd != reinterpret_cast<HWND>(m_window->winId()))
        return false;

    switch (msg->message) {
    case WM_NCCALCSIZE: {
        if (!msg->wParam)
            return false;
        auto *params = reinterpret_cast<NCCALCSIZE_PARAMS *>(msg->lParam);
        if (::IsZoomed(msg->hwnd)) {
            // A maximised window still carries the frame thickness outside the
            // work area; pin the client rect to the work area so nothing sits
            // under the taskbar.
            const RECT work = workArea(msg->hwnd);
            params->rgrc[0] = work;
        }
        *result = 0;
        return true;
    }

    case WM_NCHITTEST: {
        const LONG x = GET_X_LPARAM(msg->lParam);
        const LONG y = GET_Y_LPARAM(msg->lParam);
        RECT r{};
        ::GetWindowRect(msg->hwnd, &r);

        if (!::IsZoomed(msg->hwnd)) {
            const double scale = ::GetDpiForWindow(msg->hwnd) / 96.0;
            const int b = qMax(2, int(kResizeBorderDip * scale));

            const bool left = x < r.left + b;
            const bool right = x >= r.right - b;
            const bool top = y < r.top + b;
            const bool bottom = y >= r.bottom - b;

            if (top && left) { *result = HTTOPLEFT; return true; }
            if (top && right) { *result = HTTOPRIGHT; return true; }
            if (bottom && left) { *result = HTBOTTOMLEFT; return true; }
            if (bottom && right) { *result = HTBOTTOMRIGHT; return true; }
            if (left) { *result = HTLEFT; return true; }
            if (right) { *result = HTRIGHT; return true; }
            if (top) { *result = HTTOP; return true; }
            if (bottom) { *result = HTBOTTOM; return true; }
        }

        // Everything else is client area: QML decides what is a title bar and
        // calls startSystemMove(), which keeps snap layouts working.
        *result = HTCLIENT;
        return true;
    }

    case WM_GETMINMAXINFO: {
        auto *info = reinterpret_cast<MINMAXINFO *>(msg->lParam);
        const double scale = ::GetDpiForWindow(msg->hwnd) / 96.0;
        const QSize min = m_window->minimumSize();
        if (min.isValid()) {
            info->ptMinTrackSize.x = int(min.width() * scale);
            info->ptMinTrackSize.y = int(min.height() * scale);
        }
        return true;
    }

    case WM_NCACTIVATE:
        // Suppress the default non-client repaint, which would flash a caption.
        *result = TRUE;
        return true;

    case WM_NCPAINT:
        *result = 0;
        return true;

    default:
        break;
    }
#endif
    Q_UNUSED(result);
    return false;
}

void FramelessWindow::toggleMaximize()
{
    if (!m_window)
        return;
    if (m_window->visibility() == QWindow::Maximized)
        m_window->showNormal();
    else
        m_window->showMaximized();
}

bool FramelessWindow::maximized() const
{
    return m_window && m_window->visibility() == QWindow::Maximized;
}
