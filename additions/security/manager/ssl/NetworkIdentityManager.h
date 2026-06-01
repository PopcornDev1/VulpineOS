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
