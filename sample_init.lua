print("Sample lua config file")

if _G.GitTag == nil then
    print("Run from CLI")
    GitTag = ""
end

if _G.BaseURL == nil then
    BaseURL = "http://localhost:3210"
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

