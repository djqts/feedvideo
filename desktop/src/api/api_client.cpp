#include "api_client.h"
#include "cookie_jar.h"

#include <QDebug>
#include <QFile>
#include <QFileInfo>
#include <QHttpMultiPart>
#include <QJsonArray>
#include <QJsonObject>
#include <QNetworkAccessManager>
#include <QNetworkCookie>
#include <QNetworkReply>
#include <QNetworkRequest>
#include <QSettings>
#include <QTimer>
#include <QUrlQuery>

namespace
{
constexpr int kTimeoutMs = 30000;

QString errorTextFromBody(const QJsonDocument &doc)
{
    if (doc.isObject()) {
        const auto obj = doc.object();
        const auto err = obj.value(QStringLiteral("error"));
        if (err.isObject()) {
            const auto e = err.toObject();
            const QString msg = e.value(QStringLiteral("message")).toString();
            if (!msg.isEmpty())
                return msg;
            return e.value(QStringLiteral("code")).toString();
        }
        if (err.isString() && !err.toString().isEmpty())
            return err.toString();
        const QString msg = obj.value(QStringLiteral("message")).toString();
        if (!msg.isEmpty())
            return msg;
        return obj.value(QStringLiteral("msg")).toString();
    }
    return {};
}

QString errorCodeFromBody(const QJsonDocument &doc)
{
    if (!doc.isObject())
        return {};
    const auto obj = doc.object();
    const auto err = obj.value(QStringLiteral("error"));
    if (err.isObject())
        return err.toObject().value(QStringLiteral("code")).toString();
    return obj.value(QStringLiteral("code")).toString();
}
} // namespace

ApiClient::ApiClient(QObject *parent)
    : QObject(parent)
{
    // Hyper-V reserves 8080 on this workstation; 18080 is the local gateway port.
    const QString fromEnv = qEnvironmentVariable("FEEDVIDEO_API");
    if (!fromEnv.isEmpty())
        m_baseUrl = fromEnv;
    const QString mediaFromEnv = qEnvironmentVariable("FEEDVIDEO_MEDIA");
    if (!mediaFromEnv.isEmpty())
        m_mediaBase = mediaFromEnv;

    m_nam = new QNetworkAccessManager(this);
    m_jar = new CookieJar(m_nam);
    m_nam->setCookieJar(m_jar);
    m_nam->setAutoDeleteReplies(false); // we delete after reading, see handleReply
}

QString ApiClient::sessionStore() const
{
    return m_jar ? m_jar->storageDescription() : QString();
}

void ApiClient::setBaseUrl(const QString &url)
{
    QString normalized = url.trimmed();
    while (normalized.endsWith(QLatin1Char('/')))
        normalized.chop(1);
    if (normalized == m_baseUrl)
        return;
    m_baseUrl = normalized;
    emit baseUrlChanged();
}

void ApiClient::setMediaBase(const QString &url)
{
    QString normalized = url.trimmed();
    while (normalized.endsWith(QLatin1Char('/')))
        normalized.chop(1);
    if (normalized == m_mediaBase)
        return;
    m_mediaBase = normalized;
    emit mediaBaseChanged();
}

void ApiClient::setState(int state)
{
    if (state == m_state)
        return;
    m_state = state;
    emit stateChanged();
}

void ApiClient::setLastError(const QString &error)
{
    if (error == m_lastError)
        return;
    m_lastError = error;
    emit lastErrorChanged();
}

QString ApiClient::userName() const
{
    // /v1/auth/me only returns { uid, role } — there is no display name in the
    // session, so the id is the honest label.
    const auto candidates = {QStringLiteral("username"), QStringLiteral("nickname"), QStringLiteral("name")};
    for (const auto &key : candidates) {
        const QString value = m_me.value(key).toString();
        if (!value.isEmpty())
            return value;
    }
    const QString uid = m_me.value(QStringLiteral("uid")).toString();
    if (!uid.isEmpty())
        return tr("账号 %1").arg(uid);
    return {};
}

QString ApiClient::csrfToken() const
{
    const auto jar = m_nam->cookieJar();
    if (!jar)
        return {};
    const QUrl base(m_baseUrl);
    for (const auto &cookie : jar->cookiesForUrl(base)) {
        if (cookie.name() == "csrf_token")
            return QString::fromUtf8(cookie.value());
    }
    return {};
}

QUrl ApiClient::endpoint(const QString &path, const QVariantMap &query) const
{
    QUrl url(m_baseUrl + path);
    if (!query.isEmpty()) {
        QUrlQuery q;
        for (auto it = query.constBegin(); it != query.constEnd(); ++it) {
            if (it.value().toString().isEmpty())
                continue;
            q.addQueryItem(it.key(), it.value().toString());
        }
        url.setQuery(q);
    }
    return url;
}

QNetworkReply *ApiClient::send(const QString &tag, const QString &method, const QUrl &url, const QByteArray &body,
                               const QString &contentType, const QVariantMap &headers)
{
    QNetworkRequest request(url);
    request.setAttribute(QNetworkRequest::RedirectPolicyAttribute, QNetworkRequest::NoLessSafeRedirectPolicy);
    request.setTransferTimeout(kTimeoutMs);
    if (!contentType.isEmpty())
        request.setHeader(QNetworkRequest::ContentTypeHeader, contentType);
    const QString token = csrfToken();
    if (!token.isEmpty())
        request.setRawHeader("X-CSRF-Token", token.toUtf8());
    request.setRawHeader("Accept", "application/json");
    for (auto it = headers.constBegin(); it != headers.constEnd(); ++it)
        request.setRawHeader(it.key().toUtf8(), it.value().toString().toUtf8());

    if (tag != QLatin1String("auth.refresh"))
        m_pending.insert(tag, Pending{tag, method, url, body, contentType, headers});

    QNetworkReply *reply = nullptr;
    if (method == QLatin1String("GET"))        reply = m_nam->get(request);
    else if (method == QLatin1String("POST"))
        reply = m_nam->post(request, body);
    else if (method == QLatin1String("PUT"))
        reply = m_nam->put(request, body);
    else if (method == QLatin1String("DELETE"))
        reply = m_nam->deleteResource(request);
    else if (method == QLatin1String("PATCH"))
        reply = m_nam->sendCustomRequest(request, "PATCH", body);
    else
        reply = m_nam->get(request);

    QObject::connect(reply, &QNetworkReply::finished, this, [this, reply, tag]() { handleReply(reply, tag); });
    return reply;
}

void ApiClient::handleReply(QNetworkReply *reply, const QString &tag)
{
    reply->deleteLater();
    const int status = reply->attribute(QNetworkRequest::HttpStatusCodeAttribute).toInt();
    const QByteArray raw = reply->readAll();
    const QJsonDocument doc = QJsonDocument::fromJson(raw);

    if (reply->error() != QNetworkReply::NoError && status == 0) {
        setState(Offline);
        setLastError(tr("连不上服务端"));
        emit failed(tag, 0, QStringLiteral("NETWORK"), reply->errorString());
        return;
    }

    if (status == 401) {
        // The server answered, so the connection is fine; only the session is
        // missing. Keep the two facts separate or the shell lies about both.
        setState(Ready);

        if (tag != QLatin1String("auth.refresh") && !m_replayed.contains(tag) && m_pending.contains(tag)) {
            m_replayed.insert(tag);
            if (!m_refreshing) {
                m_refreshing = true;
                refreshSession();
            }
            return; // wait for the refresh, then replay
        }

        if (!m_me.isEmpty()) {
            m_me.clear();
            emit sessionChanged();
        }
        emit failed(tag, status, QStringLiteral("UNAUTHORIZED"), tr("需要重新登录"));
        return;
    }

    if (status >= 200 && status < 300) {
        setState(Ready);
        setLastError({});
        if (tag == QLatin1String("auth.refresh")) {
            m_refreshing = false;
            replayAfterRefresh();
        }
        const QVariant payload = doc.isNull() ? QVariant() : doc.toVariant();
        if (tag == QLatin1String("auth.me")) {
            const QVariantMap map = payload.toMap();
            const QVariant inner = map.value(QStringLiteral("data"));
            applyMe(inner.toMap().isEmpty() ? map : inner.toMap());
        }
        emit result(tag, payload);
        return;
    }

    const QString message = errorTextFromBody(doc);
    const QString code = errorCodeFromBody(doc);

    if (tag == QLatin1String("auth.refresh")) {
        // The refresh cookie is gone or rejected: the session is over.
        m_refreshing = false;
        m_replayed.clear();
        if (!m_me.isEmpty()) {
            m_me.clear();
            emit sessionChanged();
        }
    }

    emit failed(tag, status, code, message.isEmpty() ? tr("请求失败") : message);
}

void ApiClient::replayAfterRefresh()
{
    const QSet<QString> tags = m_replayed;
    m_replayed.clear();
    for (const QString &tag : tags) {
        if (!m_pending.contains(tag))
            continue;
        const Pending pending = m_pending.value(tag);
        send(pending.tag, pending.method, pending.url, pending.body, pending.contentType, pending.headers);
    }
}

void ApiClient::request(const QString &tag, const QString &method, const QString &path, const QVariantMap &body)
{
    const QByteArray payload = body.isEmpty() ? QByteArray() : QJsonDocument(QJsonObject::fromVariantMap(body)).toJson(QJsonDocument::Compact);
    send(tag, method, endpoint(path, {}), payload, QStringLiteral("application/json"));
}

void ApiClient::requestWithHeaders(const QString &tag, const QString &method, const QString &path,
                                   const QVariantMap &body, const QVariantMap &headers)
{
    const QByteArray payload = body.isEmpty() ? QByteArray() : QJsonDocument(QJsonObject::fromVariantMap(body)).toJson(QJsonDocument::Compact);
    send(tag, method, endpoint(path, {}), payload, QStringLiteral("application/json"), headers);
}

void ApiClient::requestQuery(const QString &tag, const QString &path, const QVariantMap &query)
{
    send(tag, QStringLiteral("GET"), endpoint(path, query), {}, {});
}

void ApiClient::probe()
{
    setState(Probing);
    QUrl health(m_baseUrl);
    QString path = health.path();
    // baseUrl normally ends with /api; /livez lives at the gateway root.
    while (path.endsWith(QLatin1Char('/')))
        path.chop(1);
    if (path.endsWith(QLatin1String("/api")))
        path.chop(4);
    health.setPath(path + QStringLiteral("/livez"));

    QNetworkRequest request(health);
    request.setTransferTimeout(8000);
    auto *reply = m_nam->get(request);
    QObject::connect(reply, &QNetworkReply::finished, this, [this, reply]() {
        reply->deleteLater();
        const int status = reply->attribute(QNetworkRequest::HttpStatusCodeAttribute).toInt();
        if (status >= 200 && status < 300) {
            setState(Ready);
            setLastError({});
            fetchMe();
        } else {
            setState(Offline);
            setLastError(tr("服务端没有响应"));
            emit failed(QStringLiteral("probe"), status, QStringLiteral("OFFLINE"), tr("服务端没有响应"));
        }
    });
}

QUrl ApiClient::originUrl(const QString &path) const
{
    QString base = m_baseUrl;
    if (base.endsWith(QLatin1String("/api")))
        base.chop(4);
    return QUrl(base + path);
}

void ApiClient::login(const QString &username, const QString &password)
{
    // The gateway serves auth at the origin root (/login), not under /api/v1.
    QVariantMap body;
    body.insert(QStringLiteral("username"), username);
    body.insert(QStringLiteral("password"), password);
    const QByteArray payload = QJsonDocument(QJsonObject::fromVariantMap(body)).toJson(QJsonDocument::Compact);
    send(QStringLiteral("auth.login"), QStringLiteral("POST"), originUrl(QStringLiteral("/login")), payload,
         QStringLiteral("application/json"));
}

void ApiClient::registerAccount(const QString &username, const QString &password)
{
    QVariantMap body;
    body.insert(QStringLiteral("username"), username);
    body.insert(QStringLiteral("password"), password);
    const QByteArray payload = QJsonDocument(QJsonObject::fromVariantMap(body)).toJson(QJsonDocument::Compact);
    send(QStringLiteral("auth.register"), QStringLiteral("POST"), originUrl(QStringLiteral("/register")), payload,
         QStringLiteral("application/json"));
}

void ApiClient::fetchMe()
{
    request(QStringLiteral("auth.me"), QStringLiteral("GET"), QStringLiteral("/v1/auth/me"));
}

void ApiClient::applyMe(const QVariantMap &me)
{
    if (me == m_me)
        return;
    m_me = me;
    // The refresh contract wants the last verified uid as an identity hint.
    const QString uid = me.value(QStringLiteral("uid")).toString();
    QSettings settings;
    if (uid.isEmpty())
        settings.remove(QStringLiteral("session/uid"));
    else
        settings.setValue(QStringLiteral("session/uid"), uid);
    emit sessionChanged();
}

void ApiClient::refreshSession()
{
    QSettings settings;
    const QString uid = settings.value(QStringLiteral("session/uid")).toString();
    QVariantMap body;
    if (!uid.isEmpty())
        body.insert(QStringLiteral("user_id"), uid);
    const QByteArray payload = QJsonDocument(QJsonObject::fromVariantMap(body)).toJson(QJsonDocument::Compact);
    send(QStringLiteral("auth.refresh"), QStringLiteral("POST"), originUrl(QStringLiteral("/refresh-token")), payload,
         QStringLiteral("application/json"));
}

void ApiClient::logout()
{
    send(QStringLiteral("auth.logout"), QStringLiteral("POST"), originUrl(QStringLiteral("/logout")), {},
         QStringLiteral("application/json"));
    m_me.clear();
    emit sessionChanged();
}

void ApiClient::upload(const QString &tag, const QString &videoPath, const QString &coverPath)
{
    QFile *video = new QFile(videoPath);
    if (!video->open(QIODevice::ReadOnly)) {
        emit failed(tag, 0, QStringLiteral("FILE"), tr("读不到这个文件"));
        delete video;
        return;
    }

    auto *multipart = new QHttpMultiPart(QHttpMultiPart::FormDataType);
    multipart->setBoundary("----FeedVideoDesktop");

    QHttpPart videoPart;
    const QString videoName = QFileInfo(videoPath).fileName();
    videoPart.setHeader(QNetworkRequest::ContentDispositionHeader,
                        QVariant::fromValue(QStringLiteral("form-data; name=\"video\"; filename=\"%1\"").arg(videoName)));
    videoPart.setHeader(QNetworkRequest::ContentTypeHeader, QVariant::fromValue(QStringLiteral("video/mp4")));
    videoPart.setBodyDevice(video);
    video->setParent(multipart);
    multipart->append(videoPart);

    QFile *cover = nullptr;
    if (!coverPath.isEmpty()) {
        cover = new QFile(coverPath);
        if (cover->open(QIODevice::ReadOnly)) {
            QHttpPart coverPart;
            const QString coverName = QFileInfo(coverPath).fileName();
            coverPart.setHeader(QNetworkRequest::ContentDispositionHeader,
                                QVariant::fromValue(QStringLiteral("form-data; name=\"cover\"; filename=\"%1\"").arg(coverName)));
            coverPart.setHeader(QNetworkRequest::ContentTypeHeader, QVariant::fromValue(QStringLiteral("image/jpeg")));
            coverPart.setBodyDevice(cover);
            cover->setParent(multipart);
            multipart->append(coverPart);
        } else {
            delete cover;
            cover = nullptr;
        }
    }

    QNetworkRequest request(endpoint(QStringLiteral("/v1/upload"), {}));
    request.setTransferTimeout(600000);
    const QString token = csrfToken();
    if (!token.isEmpty())
        request.setRawHeader("X-CSRF-Token", token.toUtf8());

    auto *reply = m_nam->post(request, multipart);
    multipart->setParent(reply);

    QObject::connect(reply, &QNetworkReply::uploadProgress, this, [this, tag](qint64 sent, qint64 total) {
        if (total > 0)
            emit uploadProgress(tag, qreal(sent) / qreal(total));
    });
    QObject::connect(reply, &QNetworkReply::finished, this, [this, reply, tag]() { handleReply(reply, tag); });
}
