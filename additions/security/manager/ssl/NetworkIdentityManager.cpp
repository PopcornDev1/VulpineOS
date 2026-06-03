// PUBLIC DEFAULT — see NetworkIdentityManager.h.
//
// Self-contained open-source default. Alternate builds replace this file (and
// its header) via their build overlay at build time. Do NOT paste an
// overlay-provided implementation here: the public and overlay headers declare
// different signatures, so copying the overlay .cpp over this one will fail to
// compile against the public header — and would leak build-specific code into
// the public repository.
#include "NetworkIdentityManager.h"

#include "mozilla/Logging.h"
#include "prprf.h"
#include "sslexp.h"

static mozilla::LazyLogModule sNetworkIdLog("NetworkIdentityManager");

namespace mozilla {
namespace psm {

static PRFileDesc* OpenIdentityFile() {
  const char* home = PR_GetEnv("HOME");
  if (!home) {
    return nullptr;
  }

  char path[512];
  PR_snprintf(path, sizeof(path), "%s/.vulpineos/network-identities.dat", home);

  return PR_Open(path, PR_RDONLY, 0);
}

void NetworkIdentityManager::ApplyIdentityToSocket(PRFileDesc* fd,
                                                    uint64_t contextId) {
  PRFileDesc* idFile = OpenIdentityFile();
  if (!idFile) {
    return;
  }

  char buf[4096];
  PRInt32 bytesRead = PR_Read(idFile, buf, sizeof(buf) - 1);
  PR_Close(idFile);

  if (bytesRead <= 0) {
    return;
  }
  buf[bytesRead] = '\0';

  if (bytesRead == 2 && buf[0] == '{' && buf[1] == '}') {
    return;
  }

  static const PRUint16 kExtOrder[] = {
      0x0000,  // server_name
      0x0013,  // supported_groups
      0x002b,  // extended_master_secret
      0x000d,  // signature_algorithms
      0x0010,  // application_layer_protocol_negotiation
      0x002d,  // pre_shared_key
      0x0033,  // key_share
      0x4469,  // padding
  };
  static const unsigned int kExtCount =
      sizeof(kExtOrder) / sizeof(kExtOrder[0]);

  SECStatus srv = SSL_IdentityExtensionOrderSet(
      fd, const_cast<PRUint16*>(kExtOrder), kExtCount);
  if (srv != SECSuccess) {
    MOZ_LOG(sNetworkIdLog, LogLevel::Warning,
            ("SSL_IdentityExtensionOrderSet failed"));
  }
}

}  // namespace psm
}  // namespace mozilla
