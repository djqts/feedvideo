#pragma once

#include <QByteArray>

// Tiny wrapper around the platform's per-user secret protection.
//
// On Windows this is DPAPI (CryptProtectData) with an application entropy
// string: the blob can only be read back by the same Windows user on the same
// machine, and no key is stored anywhere. Elsewhere it degrades to plain bytes,
// which callers must treat as "not secret" (the fallback path says so).
namespace SecretBox
{
bool available();
QByteArray seal(const QByteArray &plain, const QByteArray &entropy);
QByteArray open(const QByteArray &sealed, const QByteArray &entropy);
} // namespace SecretBox
