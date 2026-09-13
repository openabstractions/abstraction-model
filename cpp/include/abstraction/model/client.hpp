#pragma once
#include <abstraction/model/api/rec.h>
#include <abstraction/ipc/frame.hpp>
#include <algorithm>
#include <optional>

namespace abstraction::model {
namespace detail {
inline void validate(const api::LookupResult& result) {
    if (result.outcome != "resolved") {
        if (result.request || (result.outcome != "invalid" && result.outcome != "unavailable" &&
                result.outcome != "unsupported_mapping" && result.outcome != "forbidden"))
            throw api::ServiceError("invalid_model", "inconsistent lookup refusal");
        return;
    }
    if (!result.request) throw api::ServiceError("invalid_model", "resolved lookup lacks request");
    const auto& request = *result.request;
    const auto& digest = request.artifact.digest;
    if (digest.size() != 71 || digest.compare(0, 7, "sha256:") != 0 ||
        !std::all_of(digest.begin()+7, digest.end(), [](unsigned char c) {
            return (c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F');
        }) || request.artifact.size < 0 || request.sources.empty())
        throw api::ServiceError("invalid_model", "invalid resolved artifact");
    for (const auto& source : request.sources) {
        const auto prefix = source.scheme + "://";
        auto actual = source.locator.substr(0, prefix.size());
        std::transform(actual.begin(), actual.end(), actual.begin(), [](unsigned char c) {
            return static_cast<char>(c >= 'A' && c <= 'Z' ? c + ('a'-'A') : c);
        });
        if ((source.scheme != "http" && source.scheme != "https") || actual != prefix)
            throw api::ServiceError("invalid_model", "anonymous HTTP(S) source required");
        const auto end = source.locator.find_first_of("/?#", prefix.size());
        const auto authority = source.locator.substr(prefix.size(),end == std::string::npos ? end : end-prefix.size());
        if (authority.empty() || authority.find('@') != std::string::npos ||
            source.locator.find_first_of("\r\n\t") != std::string::npos || source.locator.find('\0') != std::string::npos)
            throw api::ServiceError("invalid_model", "invalid anonymous source");
    }
}
}
// Binds the generated read-only interface to one explicit shared-IPC endpoint.
// Each ordinary call gets a fresh five-second waiting budget.
class Client : public api::ModelResolver {
public:
    explicit Client(std::string endpoint) : endpoint_(std::move(endpoint)) {}
    Client(std::string endpoint, ipc::Deadline deadline) : endpoint_(std::move(endpoint)), deadline_(deadline) {}
    Client WithDeadline(ipc::Deadline deadline) const {
        auto scoped = *this; scoped.deadline_ = deadline; return scoped;
    }
    Client WithServerExpectation(std::optional<ipc::ServerExpectation> server) const {auto copy=*this;copy.server_=std::move(server);return copy;}
 Client WithCancellation(ipc::CancellationToken cancellation) const {
        auto scoped = *this; scoped.cancellation_ = std::move(cancellation); return scoped;
    }
    api::LookupResult Resolve(const api::Ref& ref) override {
        auto transport = deadline_ ? ipc::FrameTransport(endpoint_, *deadline_, 1u<<20)
                                   : ipc::FrameTransport(endpoint_, 5000, 1u<<20);
        transport = transport.WithCancellation(cancellation_).WithServerExpectation(server_);
        api::ModelResolverClient<ipc::FrameTransport> client(transport);
        auto result = client.Resolve(ref);
        detail::validate(result);
        return result;
    }
private:
    std::string endpoint_;
    std::optional<ipc::Deadline> deadline_;
    ipc::CancellationToken cancellation_;
 std::optional<ipc::ServerExpectation> server_;
};
}
