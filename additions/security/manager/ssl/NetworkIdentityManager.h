// PUBLIC DEFAULT — self-contained placeholder.
//
// This is the open-source default NetworkIdentityManager shipped in this
// repository. Alternate builds may provide their own NetworkIdentityManager
// (a richer interface) through their build overlay, which REPLACES both this
// header and the matching .cpp in the source tree at build time.
//
// Therefore:
//   * Keep this file self-contained and behavior-minimal.
//   * Do NOT copy an overlay-provided implementation into this file — those
//     implementations are supplied at build time and must not be committed to
//     this public repository. The public header and the overlay header
//     intentionally differ; mixing them produces a signature mismatch.
#ifndef NetworkIdentityManager_h__
#define NetworkIdentityManager_h__

#include "prtypes.h"
#include "prio.h"

namespace mozilla {
namespace psm {

class NetworkIdentityManager {
 public:
  static void ApplyIdentityToSocket(PRFileDesc* fd, uint64_t contextId);
};

}  // namespace psm
}  // namespace mozilla

#endif
