print("Sample lua config file")

if _G.GitTag == nil then
    print("Run from CLI")
    GitTag = ""
end

do
    local envBase = os.getenv("BASE_URL")
    if envBase ~= nil and envBase ~= "" then
        BaseURL = envBase
    elseif _G.BaseURL == nil then
        BaseURL = "http://localhost:3210"
    end
end

local function getEnv(name, default)
    local value = os.getenv(name)
    if value == nil then
        return default
    end
    return value
end

Address = ":3210"
GitHubClientID = getEnv("GITHUB_CLIENT_ID", "")
GitHubClientSecret = getEnv("GITHUB_CLIENT_SECRET", "")

print("Version: " .. GitTag)
print("BaseURL: " .. BaseURL)
