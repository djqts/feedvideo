#include "cookie_jar.h"
#include "secret_box.h"

#include <QCoreApplication>
#include <QDir>
#include <QFile>
#include <QFileInfo>
#include <QJsonArray>
#include <QJsonDocument>
#include <QNetworkCookie>
#include <QStandardPaths>
#include <QDebug>

namespace
{
const QByteArray kEntropy = QByteArrayLiteral("FeedVideoDesktop/session/v1");
}

CookieJar::CookieJar(QObject *parent)
    : QNetworkCookieJar(parent)
{
    const QString override = qEnvironmentVariable("FEEDVIDEO_STATE_DIR");
    const QString stateDir = override.isEmpty()
        ? QCoreApplication::applicationDirPath() + QStringLiteral("/.session")
        : override;

    m_primaryPath = QStandardPaths::writableLocation(QStandardPaths::AppDataLocation)
        + QStringLiteral("/session.cookies");
    m_fallbackPath = stateDir + QStringLiteral("/session.bin");
    load();
}

CookieJar::~CookieJar()
{
    save();
}

QString CookieJar::storageDescription() const
{
    if (m_usingFallback)
        return tr("工作区加密副本：%1").arg(m_fallbackPath);
    return tr("用户目录：%1").arg(m_primaryPath);
}

void CookieJar::load()
{
    QFile primary(m_primaryPath);
    if (primary.exists() && primary.open(QIODevice::ReadOnly)) {
        applyJson(primary.readAll());
        m_usingFallback = false;
        return;
    }

    QFile fallback(m_fallbackPath);
    if (!fallback.exists() || !fallback.open(QIODevice::ReadOnly))
        return;

    const QByteArray raw = fallback.readAll();
    const QByteArray plain = SecretBox::open(raw, kEntropy);
    if (plain.isEmpty()) {
        // Sealed on another machine/user (or written before sealing existed).
        applyJson(raw);
    } else {
        applyJson(plain);
    }
    m_usingFallback = true;
}

void CookieJar::applyJson(const QByteArray &json)
{
    const QJsonArray entries = QJsonDocument::fromJson(json).array();
    for (const auto &entry : entries) {
        const auto cookies = QNetworkCookie::parseCookies(entry.toString().toUtf8());
        for (const auto &cookie : cookies)
            QNetworkCookieJar::insertCookie(cookie);
    }
}

bool CookieJar::writeFile(const QString &path, const QByteArray &bytes) const
{
    if (!QDir().mkpath(QFileInfo(path).absolutePath()))
        return false;
    QFile file(path);
    if (!file.open(QIODevice::WriteOnly | QIODevice::Truncate))
        return false;
    return file.write(bytes) == bytes.size();
}

void CookieJar::save() const
{
    QJsonArray entries;
    const auto cookies = allCookies();
    for (const auto &cookie : cookies)
        entries.append(QString::fromUtf8(cookie.toRawForm()));
    const QByteArray json = QJsonDocument(entries).toJson(QJsonDocument::Compact);

    if (writeFile(m_primaryPath, json)) {
        m_usingFallback = false;
        // A stale fallback must not shadow the primary on the next start.
        if (QFile::exists(m_fallbackPath))
            QFile::remove(m_fallbackPath);
        return;
    }

    const QByteArray sealed = SecretBox::seal(json, kEntropy);
    m_fallbackSealed = !sealed.isEmpty() && SecretBox::available();
    const QByteArray payload = m_fallbackSealed ? sealed : json;
    if (writeFile(m_fallbackPath, payload)) {
        m_usingFallback = true;
    } else {
        qWarning() << "cookie jar: neither" << m_primaryPath << "nor" << m_fallbackPath << "is writable";
    }
}

bool CookieJar::insertCookie(const QNetworkCookie &cookie)
{
    const bool inserted = QNetworkCookieJar::insertCookie(cookie);
    if (inserted)
        save();
    return inserted;
}

bool CookieJar::deleteCookie(const QNetworkCookie &cookie)
{
    const bool deleted = QNetworkCookieJar::deleteCookie(cookie);
    if (deleted)
        save();
    return deleted;
}

bool CookieJar::setCookiesFromUrl(const QList<QNetworkCookie> &cookieList, const QUrl &url)
{
    const bool changed = QNetworkCookieJar::setCookiesFromUrl(cookieList, url);
    if (changed)
        save();
    return changed;
}

void CookieJar::clear()
{
    setAllCookies({});
    save();
}
