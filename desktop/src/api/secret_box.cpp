#include "secret_box.h"

#ifdef Q_OS_WIN
#  include <windows.h>
#  include <dpapi.h>
#endif

namespace
{
#ifdef Q_OS_WIN
QByteArray blobToBytes(const DATA_BLOB &blob)
{
    QByteArray out(reinterpret_cast<const char *>(blob.pbData), int(blob.cbData));
    ::LocalFree(blob.pbData);
    return out;
}
#endif
} // namespace

namespace SecretBox
{
bool available()
{
#ifdef Q_OS_WIN
    return true;
#else
    return false;
#endif
}

QByteArray seal(const QByteArray &plain, const QByteArray &entropy)
{
#ifdef Q_OS_WIN
    DATA_BLOB in{};
    in.pbData = reinterpret_cast<BYTE *>(const_cast<char *>(plain.constData()));
    in.cbData = DWORD(plain.size());
    DATA_BLOB extra{};
    extra.pbData = reinterpret_cast<BYTE *>(const_cast<char *>(entropy.constData()));
    extra.cbData = DWORD(entropy.size());

    DATA_BLOB out{};
    if (!::CryptProtectData(&in, L"FeedVideo desktop session", &extra, nullptr, nullptr,
                            CRYPTPROTECT_UI_FORBIDDEN, &out))
        return {};
    return blobToBytes(out);
#else
    Q_UNUSED(entropy);
    return plain;
#endif
}

QByteArray open(const QByteArray &sealed, const QByteArray &entropy)
{
#ifdef Q_OS_WIN
    DATA_BLOB in{};
    in.pbData = reinterpret_cast<BYTE *>(const_cast<char *>(sealed.constData()));
    in.cbData = DWORD(sealed.size());
    DATA_BLOB extra{};
    extra.pbData = reinterpret_cast<BYTE *>(const_cast<char *>(entropy.constData()));
    extra.cbData = DWORD(entropy.size());

    DATA_BLOB out{};
    if (!::CryptUnprotectData(&in, nullptr, &extra, nullptr, nullptr,
                              CRYPTPROTECT_UI_FORBIDDEN, &out))
        return {};
    return blobToBytes(out);
#else
    Q_UNUSED(entropy);
    return sealed;
#endif
}
} // namespace SecretBox
