import json
import urllib.parse
import urllib.request

def get_articles():
    """
    Executes a SPARQL query to get articles from Wikidata and saves them to a file.
    """
    with open("queries/query.sparql", "r") as f:
        query = f.read()

    endpoint = "https://query.wikidata.org/sparql"
    params = {"query": query, "format": "json"}
    url = endpoint + "?" + urllib.parse.urlencode(params)

    with urllib.request.urlopen(url) as response:
        data = json.loads(response.read().decode())

    articles = [item["article"]["value"] for item in data["results"]["bindings"]]

    output_data = {"articles": articles}

    with open("data/articles.json", "w") as f:
        json.dump(output_data, f, indent=2)
