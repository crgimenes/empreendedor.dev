print("Sample lua config file")

if _G.GitTag == nil then
    print("Run from CLI")
    GitTag = ""
end

print("Version: " .. GitTag)
Address = ":3210"

