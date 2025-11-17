import json
import urllib.parse
import urllib.request
import re # Import re module

# Helper function to check if a string is an IP address
def _is_ip_address(user_string):
    # IPv4 pattern
    ipv4_pattern = re.compile(r"^\d{1,3}\.\d{1,3}\.\d{1,3}\.\d{1,3}$")
    # Simple IPv6 pattern (more complex patterns exist, but this covers common cases)
    # Checks for at least one colon, and optionally square brackets for MediaWiki
    ipv6_pattern = re.compile(r"^\[?([0-9a-fA-F:]+){1,4}(:[0-9a-fA-F]{1,4}){1,4}\]?$")

    if ipv4_pattern.match(user_string):
        return True
    if ipv6_pattern.match(user_string):
        return True
    return False

def get_revisions():
    """
    Fetches the revision history of articles listed in articles.json and saves it to a JSON file.
    """
    with open("data/articles.json", "r") as f:
        articles_data = json.load(f)
        articles = articles_data["articles"]

    all_revisions_data = []

    for article_url in articles:
        print(f"Fetching revisions for {article_url}...")
        article_revisions = {
            "article_url": article_url,
            "revisions": []
        }
        title = urllib.parse.unquote(article_url.split("/")[-1])

        # Step 1: Get all revision timestamps, users and ids
        revisions_info = []
        api_url = "https://zh.wikipedia.org/w/api.php"
        params = {
            "action": "query",
            "prop": "revisions",
            "titles": title,
            "rvprop": "timestamp|user|ids",
            "rvlimit": "max",
            "format": "json",
        }

        while True:
            url = api_url + "?" + urllib.parse.urlencode(params)
            req = urllib.request.Request(url, headers={"User-Agent": "MyResearchBot/1.0 (Researcher)"})
            with urllib.request.urlopen(req) as response:
                data = json.loads(response.read().decode())

            page_id = list(data["query"]["pages"].keys())[0]
            if page_id == "-1":
                print(f"  Article not found: {title}")
                break
            
            revisions = data["query"]["pages"][page_id].get("revisions", [])
            revisions_info.extend(revisions)

            if "continue" in data:
                params.update(data["continue"])
            else:
                break

        # Step 2: Get diff for each revision
        for i in range(len(revisions_info)):
            rev = revisions_info[i]
            user = rev.get("user", "N/A")
            timestamp = rev.get("timestamp", "N/A")
            revid = rev.get("revid")

            diff = "N/A"
            if i + 1 < len(revisions_info):
                old_revid = revisions_info[i+1].get("revid")
                diff_params = {
                    "action": "compare",
                    "fromrev": old_revid,
                    "torev": revid,
                    "format": "json"
                }
                diff_url = api_url + "?" + urllib.parse.urlencode(diff_params)
                diff_req = urllib.request.Request(diff_url, headers={"User-Agent": "MyResearchBot/1.0 (Researcher)"})
                with urllib.request.urlopen(diff_req) as diff_response:
                    diff_data = json.loads(diff_response.read().decode())
                    diff = diff_data.get("compare", {}).get("*", "N/A")

            # Determine if the user is an IP address
            is_ip = _is_ip_address(user)

            article_revisions["revisions"].append({
                "user": user,
                "timestamp": timestamp,
                "diff": diff,
                "is_ip": is_ip # Add the new field
            })
        
        all_revisions_data.append(article_revisions)

    with open("data/revisions.json", "w", encoding="utf-8") as f:
        json.dump(all_revisions_data, f, indent=2, ensure_ascii=False)
