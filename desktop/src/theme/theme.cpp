#include "theme.h"

#include <QPointF>
#include <QSettings>

namespace
{
struct Palette
{
    QColor paper, panel, ink, ink2, rule, chip, signal, onSignal, trace, tagBg, tagFg, danger, scrim;
};

Palette paperPalette()
{
    Palette p;
    p.paper = QColor(QStringLiteral("#F4F4F2"));
    p.panel = QColor(QStringLiteral("#FFFFFF"));
    p.ink = QColor(QStringLiteral("#161616"));
    p.ink2 = QColor(QStringLiteral("#5C5C5C"));
    p.rule = QColor(QStringLiteral("#D9D9D6"));
    p.chip = QColor(QStringLiteral("#E4E4E0"));
    p.signal = QColor(QStringLiteral("#FFFA00"));
    p.onSignal = QColor(QStringLiteral("#161616"));
    p.trace = QColor(QStringLiteral("#161616")); // here = ink on paper
    p.tagBg = QColor(QStringLiteral("#161616"));
    p.tagFg = QColor(QStringLiteral("#FFFFFF"));
    p.danger = QColor(QStringLiteral("#B3261E"));
    p.scrim = QColor(22, 22, 22, 128);
    return p;
}

Palette stagePalette()
{
    Palette p;
    p.paper = QColor(QStringLiteral("#191919"));
    p.panel = QColor(QStringLiteral("#222222"));
    p.ink = QColor(QStringLiteral("#EDEDED"));
    p.ink2 = QColor(QStringLiteral("#A3A3A3"));
    p.rule = QColor(QStringLiteral("#353535"));
    p.chip = QColor(QStringLiteral("#2E2E2E"));
    p.signal = QColor(QStringLiteral("#FFFA00"));
    p.onSignal = QColor(QStringLiteral("#161616"));
    p.trace = QColor(QStringLiteral("#FFFA00")); // here = signal on stage
    p.tagBg = QColor(QStringLiteral("#3A3A3A"));
    p.tagFg = QColor(QStringLiteral("#EDEDED"));
    p.danger = QColor(QStringLiteral("#FF8A7A"));
    p.scrim = QColor(0, 0, 0, 160);
    return p;
}

struct ScreenRule
{
    const char *room;
    qreal density;
};

const QHash<QString, ScreenRule> &screenRules()
{
    static const QHash<QString, ScreenRule> rules = {
        {QStringLiteral("feed"), { "stage", 0.9375 }},
        {QStringLiteral("video"), { "stage", 1.0 }},
        {QStringLiteral("player"), { "stage", 1.0 }},
        {QStringLiteral("search"), { "paper", 1.0 }},
        {QStringLiteral("creator"), { "paper", 0.9375 }},
        {QStringLiteral("settings"), { "paper", 1.0625 }},
        {QStringLiteral("login"), { "paper", 1.0625 }},
        {QStringLiteral("publish"), { "paper", 1.0625 }},
        {QStringLiteral("notifications"), { "paper", 1.0 }},
        {QStringLiteral("collections"), { "paper", 1.0 }},
        {QStringLiteral("history"), { "paper", 1.0 }},
    };
    return rules;
}
} // namespace

Theme::Theme(QObject *parent)
    : QObject(parent)
{
    QSettings s;
    m_preference = s.value(QStringLiteral("theme/preference"), 0).toInt();
    m_reduce = s.value(QStringLiteral("a11y/reduceMotion"), false).toBool();
    refresh();
}

void Theme::setPreference(int p)
{
    if (p == m_preference)
        return;
    m_preference = p;
    QSettings s;
    s.setValue(QStringLiteral("theme/preference"), p);
    refresh();
}

void Theme::setReduceMotion(bool r)
{
    if (r == m_reduce)
        return;
    m_reduce = r;
    QSettings s;
    s.setValue(QStringLiteral("a11y/reduceMotion"), r);
    emit changed();
}

void Theme::enterScreen(const QString &screenId)
{
    const auto it = screenRules().constFind(screenId);
    const QString roomId = it != screenRules().constEnd() ? QString::fromLatin1(it->room) : QStringLiteral("paper");
    const qreal density = it != screenRules().constEnd() ? it->density : 1.0;
    const bool densityChanged = !qFuzzyCompare(density, m_density);
    m_room = roomId;
    m_density = density;
    refresh();
    if (densityChanged)
        emit changed();
}

void Theme::refresh()
{
    int next = m_mode;
    if (m_preference == 0)
        next = (m_room == QLatin1String("stage")) ? 1 : 0;
    else
        next = m_preference - 1;
    m_mode = qBound(0, next, 1);
    emit changed();
}

void Theme::toggleMode()
{
    setPreference(m_mode == 1 ? 1 : 2);
}

qreal Theme::cutFactor() const
{
    return 0.6; // the "tight" variant: every corner cut is 60% of the standard size
}

qreal Theme::cutSmall() const
{
    return 7 * cutFactor() * m_density;
}
qreal Theme::cutButton() const
{
    return 8 * cutFactor() * m_density;
}
qreal Theme::cutPanel() const
{
    return 26 * cutFactor() * m_density;
}
qreal Theme::cutFrame() const
{
    return 52 * cutFactor() * m_density;
}

QEasingCurve Theme::snap() const
{
    QEasingCurve c(QEasingCurve::BezierSpline);
    c.addCubicBezierSegment(QPointF(0.16, 1.0), QPointF(0.3, 1.0), QPointF(1.0, 1.0));
    return c;
}

QEasingCurve Theme::mech() const
{
    QEasingCurve c(QEasingCurve::BezierSpline);
    c.addCubicBezierSegment(QPointF(0.7, 0.0), QPointF(0.2, 1.0), QPointF(1.0, 1.0));
    return c;
}

QEasingCurve Theme::easeCurve() const
{
    QEasingCurve c(QEasingCurve::BezierSpline);
    c.addCubicBezierSegment(QPointF(0.2, 0.7), QPointF(0.1, 1.0), QPointF(1.0, 1.0));
    return c;
}

QString Theme::qtVersion() const
{
    return QString::fromLatin1(qVersion());
}

namespace
{
Palette current(int mode)
{
    return mode == 1 ? stagePalette() : paperPalette();
}
} // namespace

QColor Theme::paper() const
{
    return current(m_mode).paper;
}
QColor Theme::panel() const
{
    return current(m_mode).panel;
}
QColor Theme::ink() const
{
    return current(m_mode).ink;
}
QColor Theme::ink2() const
{
    return current(m_mode).ink2;
}
QColor Theme::rule() const
{
    return current(m_mode).rule;
}
QColor Theme::chip() const
{
    return current(m_mode).chip;
}
QColor Theme::signal() const
{
    return current(m_mode).signal;
}
QColor Theme::onSignal() const
{
    return current(m_mode).onSignal;
}
QColor Theme::trace() const
{
    return current(m_mode).trace;
}
QColor Theme::tagBg() const
{
    return current(m_mode).tagBg;
}
QColor Theme::tagFg() const
{
    return current(m_mode).tagFg;
}
QColor Theme::danger() const
{
    return current(m_mode).danger;
}
QColor Theme::scrim() const
{
    return current(m_mode).scrim;
}
