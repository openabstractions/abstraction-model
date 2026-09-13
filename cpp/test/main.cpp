#include <abstraction/model/api/rec.h>
#include <type_traits>
namespace model=abstraction::model::api;
namespace request=abstraction::download::request;
int main(){static_assert(std::is_same<decltype(model::LookupResult{}.request),std::optional<request::Request>>::value);model::LookupResult value;value.outcome="resolved";value.request=request::Request{};value.request->artifact.digest="sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa";value.request->sources.push_back(request::Source{"https","https://example.invalid/model"});auto decoded=model::decode_lookupresult(std::string_view(model::encode_lookupresult(value)));return decoded.request->artifact.digest==value.request->artifact.digest?0:1;}
